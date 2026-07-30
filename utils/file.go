package utils

import (
	"archive/zip"
	"bufio"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cast"
)

// FileStruct - File 结构体
type FileStruct struct {
	request  *FileRequest
	response *FileResponse
}

// FileRequest - File 请求
type FileRequest struct {
	// 文件名
	Name string
	// 文件路径（包含文件名）
	Path string
	// 目录路径（不包含文件名）
	Dir string
	// 文件后缀
	Ext string
	// 限制行数
	Limit int
	// 读取偏移量
	Page int
	// 返回结果格式
	Format string
	// 是否包含子目录
	Sub bool
	// 域名 - 用于拼接文件路径
	Domain string
	// 前缀 - 用于过滤前缀
	Prefix string
}

// FileResponse - File 响应
type FileResponse struct {
	Error  error
	Result any
	Text   string
	Byte   []byte
	Slice  []any
}

// File - 文件系统
func File(request ...FileRequest) *FileStruct {

	if len(request) == 0 {
		request = append(request, FileRequest{})
	}

	if Is.Empty(request[0].Limit) {
		request[0].Limit = 10
	}

	if Is.Empty(request[0].Page) {
		request[0].Page = 1
	}

	if Is.Empty(request[0].Format) {
		request[0].Format = "network"
	}

	if Is.Empty(request[0].Sub) {
		request[0].Sub = true
	}

	if Is.Empty(request[0].Ext) {
		request[0].Ext = "*"
	}

	return &FileStruct{
		request:  &request[0],
		response: &FileResponse{},
	}
}

// Path 设置文件路径(包含文件名，如：/tmp/test.txt)
func (this *FileStruct) Path(path any) *FileStruct {
	this.request.Path = cast.ToString(path)
	return this
}

// Dir 设置目录路径(不包含文件名，如：/tmp)
func (this *FileStruct) Dir(dir any) *FileStruct {
	this.request.Dir = cast.ToString(dir)
	return this
}

// Name 设置文件名(不包含路径，如：test.txt)
func (this *FileStruct) Name(name any) *FileStruct {
	this.request.Name = cast.ToString(name)
	return this
}

// Ext 设置文件后缀(如：.txt)
func (this *FileStruct) Ext(ext any) *FileStruct {
	this.request.Ext = cast.ToString(ext)
	return this
}

// Domain 设置域名(用于拼接文件路径)
func (this *FileStruct) Domain(domain any) *FileStruct {
	this.request.Domain = cast.ToString(domain)
	return this
}

// Prefix 设置前缀(用于过滤前缀)
func (this *FileStruct) Prefix(prefix any) *FileStruct {
	this.request.Prefix = cast.ToString(prefix)
	return this
}

// Limit 设置限制行数
func (this *FileStruct) Limit(limit any) *FileStruct {
	this.request.Limit = cast.ToInt(limit)
	return this
}

// Page 设置读取偏移量
func (this *FileStruct) Page(page any) *FileStruct {
	this.request.Page = cast.ToInt(page)
	return this
}

// Save 保存文件
func (this *FileStruct) Save(reader io.Reader, path ...string) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = path[0]
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	dir := filepath.Dir(this.request.Path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		// 目录不存在，需要创建
		if err := os.MkdirAll(dir, 0755); err != nil {
			this.response.Error = err
			return this.response
		}
	}

	// 创建文件
	file, err := os.Create(this.request.Path)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	// 关闭文件
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			this.response.Error = err
			return
		}
	}(file)

	// 将文件通过流的方式写入磁盘
	_, err = io.Copy(file, reader)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	return this.response
}

// Remove 删除文件或目录
func (this *FileStruct) Remove(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	var err error

	fileInfo, err := os.Stat(this.request.Path)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	if fileInfo.IsDir() {
		// 删除目录
		err = os.RemoveAll(this.request.Path)
	} else {
		// 删除文件
		err = os.Remove(this.request.Path)
	}

	if err != nil {
		this.response.Error = err
		return this.response
	}

	return this.response
}

// Download 下载文件
/**
 * @param path1 远程文件路径（下载地址）
 * @param path2 本地文件路径（保存路径，包含文件名）
 * @return *FileResponse
 * @example：
 * 1. item := utils.File().Download("https://inis.cn/name.zip", "public/test.zip")
 * 2. item := utils.File().Dir("public").Name("test.zip").Download("https://inis.cn/name.zip")
 * 3. item := utils.File(utils.FileRequest{
		Path: "https://inis.cn/name.zip",
		Name: "test.zip",
		Dir: "public",
	}).Download()
*/
func (this *FileStruct) Download(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	// 创建一个HTTP GET请求
	req, err := http.NewRequest("GET", this.request.Path, nil)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	// 发送HTTP请求并获取响应
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	defer resp.Body.Close()

	if Is.Empty(this.request.Name) {
		this.request.Name = filepath.Base(this.request.Path)
	}

	var savePath string
	if len(path) > 1 {
		savePath = cast.ToString(path[1])
	} else {
		savePath = filepath.Join(this.request.Dir, this.request.Name)
	}

	// 如果目录不存在，需要创建
	dir := filepath.Dir(savePath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			this.response.Error = err
			return this.response
		}
	}
	// 创建本地文件并将HTTP响应的Body写入本地文件
	saveFile, err := os.Create(savePath)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	defer func(saveFile *os.File) {
		err := saveFile.Close()
		if err != nil {
			return
		}
	}(saveFile)

	_, err = io.Copy(saveFile, resp.Body)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	return this.response
}

// Byte 获取文件字节
func (this *FileStruct) Byte(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	// 读取文件
	file, err := os.Open(this.request.Path)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			return
		}
	}(file)

	// 获取文件信息
	info, err := file.Stat()
	if err != nil {
		this.response.Error = err
		return this.response
	}
	size := info.Size()

	// 小于50MB，整体读取
	if size < 50*1024*1024 {
		bytes := make([]byte, size)
		_, err = file.Read(bytes)
		if err != nil {
			this.response.Error = err
			return this.response
		}
		this.response.Byte = bytes
		this.response.Text = string(bytes)
		this.response.Result = bytes
		return this.response
	}

	// 大于等于50MB，分块读取
	var bytes []byte
	buffer := make([]byte, 1024*1024)
	for {
		index, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			this.response.Error = err
			break
		}
		bytes = append(bytes, buffer[:index]...)
		if err == io.EOF {
			break
		}
	}

	this.response.Byte = bytes
	this.response.Text = string(bytes)
	this.response.Result = bytes

	return this.response
}

// List 获取指定目录下的所有文件
func (this *FileStruct) List(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Dir) {
		this.response.Error = errors.New("目录路径不能为空")
		return this.response
	}

	var slice []string
	this.response.Error = filepath.Walk(this.request.Dir, func(path string, info os.FileInfo, err error) error {
		// 遍历出错（如权限不足）时跳过该条目，避免 info 为 nil 崩溃
		if err != nil {
			return nil
		}
		// 忽略当前目录
		if info.IsDir() {
			return nil
		}
		// 忽略子目录
		if !this.request.Sub && filepath.Dir(path) != filepath.Clean(this.request.Dir) {
			return nil
		}
		// []string 转 []any
		var exts []any
		// this.request.Ext 逗号分隔的字符串 转 []string
		for _, val := range strings.Split(this.request.Ext, ",") {
			// 忽略空字符串
			if Is.Empty(val) {
				continue
			}
			// 去除空格
			exts = append(exts, strings.TrimSpace(val))
		}
		// 忽略指定后缀
		if !InArray("*", exts) && !InArray[any](filepath.Ext(path), exts) {
			return nil
		}
		slice = append(slice, path)
		return nil
	})

	// 转码为网络路径
	if this.request.Format == "network" {
		for key, val := range slice {
			slice[key] = filepath.ToSlash(val)
			if !Is.Empty(this.request.Domain) {
				slice[key] = this.request.Domain + slice[key][len(this.request.Prefix):]
			}
		}
	}

	for _, val := range slice {
		this.response.Slice = append(this.response.Slice, val)
	}
	this.response.Result = slice
	this.response.Text = strings.Join(slice, ",")
	this.response.Byte = []byte(this.response.Text)

	return this.response
}

// Exist 判断文件是否存在
func (this *FileStruct) Exist(path ...any) (ok bool) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Path) {
		return false
	}

	// 判断文件是否存在
	if _, err := os.Stat(this.request.Path); os.IsNotExist(err) {
		return false
	}

	return true
}

// Line 按行读取文件
func (this *FileStruct) Line(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	// 读取指定区间的行
	file, err := os.Open(this.request.Path)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			return
		}
	}(file)

	end := this.request.Page * this.request.Limit
	start := end - this.request.Limit + 1

	// 顺序扫描文件，只保留 [start, end] 区间的行
	lines := make([]string, 0)
	scanner := bufio.NewScanner(file)
	current := 0
	for scanner.Scan() {
		current++
		if current < start {
			continue
		}
		if current > end {
			break
		}
		lines = append(lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		this.response.Error = err
		return this.response
	}

	this.response.Result = lines
	this.response.Text = Json.Encode(this.response.Result)
	this.response.Byte = []byte(this.response.Text)

	for _, v := range lines {
		this.response.Slice = append(this.response.Slice, v)
	}

	return this.response
}

// DirInfo 获取目录信息
func (this *FileStruct) DirInfo(dir ...any) (result *FileResponse) {

	if len(dir) != 0 {
		this.request.Dir = cast.ToString(dir[0])
	}

	if Is.Empty(this.request.Dir) {
		this.request.Dir = "./"
	}

	// 如果目录不是以 / 结尾，则补上
	if this.request.Dir[len(this.request.Dir)-1:] != "/" {
		this.request.Dir += "/"
	}

	// 判断目录是否存在
	if _, err := os.Stat(this.request.Dir); os.IsNotExist(err) {
		this.response.Error = err
		return this.response
	}

	// 获取目录信息
	fileInfo, err := os.Stat(this.request.Dir)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	var dirs []string
	var files []string

	// 只获取当前目录下的文件夹和文件 - 忽略子目录
	fileList, err := os.ReadDir(this.request.Dir)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	for _, file := range fileList {
		path := filepath.Join(this.request.Dir, file.Name())
		// path 转网络路径
		path = filepath.ToSlash(path)
		// 替换 this.request.Dir 为空字符串
		path = strings.Replace(path, this.request.Dir, "", 1)

		if file.IsDir() {
			dirs = append(dirs, path)
		} else {
			files = append(files, path)
		}
	}

	// 获取目录信息
	this.response.Result = map[string]any{
		"info":  fileInfo,
		"dirs":  dirs,
		"files": files,
	}
	this.response.Text = Json.Encode(this.response.Result)
	this.response.Byte = []byte(this.response.Text)

	return this.response
}

// EnZip 压缩文件
/**
 * @return *FileResponse
 * @example：
 * 1. item := utils.File().Dir("public").Name("name.zip").EnZip()
 * 2. item := utils.File().Dir("public").Path("public/name.zip").EnZip()
 * 3. item := utils.File(utils.FileRequest{
		Path: "public/name.zip",
		Dir: "public",
	}).EnZip()
*/
func (this *FileStruct) EnZip() (result *FileResponse) {

	if Is.Empty(this.request.Dir) {
		this.response.Error = errors.New("压缩目录不能为空")
		return this.response
	}

	if Is.Empty(this.request.Path) && !Is.Empty(this.request.Name) {

		// 判断 Dir 是否以 / 结尾
		if this.request.Dir[len(this.request.Dir)-1:] != "/" {
			this.request.Dir += "/"
		}

		// 判断 Name 是否以 .zip 结尾
		if this.request.Name[len(this.request.Name)-4:] != ".zip" {
			this.request.Name += ".zip"
		}

		this.request.Path = this.request.Dir + this.request.Name
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("压缩后的文件路径不能为空")
		return this.response
	}

	// 判断目录是否存在
	if _, err := os.Stat(this.request.Dir); os.IsNotExist(err) {
		this.response.Error = err
		return this.response
	}

	var files []string
	err := filepath.Walk(this.request.Dir, func(path string, info os.FileInfo, err error) error {
		if !info.IsDir() {
			files = append(files, path)
		}
		this.response.Error = err
		return this.response.Error
	})
	if err != nil {
		this.response.Error = err
		return this.response
	}

	zipFile, err := os.Create(this.request.Path)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	defer func(zipFile *os.File) {
		err := zipFile.Close()
		if err != nil {
			this.response.Error = err
			return
		}
	}(zipFile)

	write := zip.NewWriter(zipFile)
	defer func(write *zip.Writer) {
		err := write.Close()
		if err != nil {
			this.response.Error = err
			return
		}
	}(write)

	for _, file := range files {

		item, err := os.Open(file)
		if err != nil {
			this.response.Error = err
			return this.response
		}
		defer func(item *os.File) {
			err := item.Close()
			if err != nil {
				this.response.Error = err
				return
			}
		}(item)

		info, err := item.Stat()
		if err != nil {
			this.response.Error = err
			return this.response
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			this.response.Error = err
			return this.response
		}

		header.Name = file
		header.Method = zip.Deflate

		writer, err := write.CreateHeader(header)
		if err != nil {
			this.response.Error = err
			return this.response
		}

		_, err = io.Copy(writer, item)
		if err != nil {
			this.response.Error = err
			return this.response
		}
	}

	this.response.Text = "1"
	this.response.Result = true
	this.response.Byte = []byte{1}

	return this.response
}

// UnZip 解压文件
/**
 * @return *FileResponse
 * @example：
 * 1. item := utils.File().Dir("public").Name("name.zip").UnZip()
 * 2. item := utils.File().Dir("public").Path("public/name.zip").UnZip()
 * 3. item := utils.File(utils.FileRequest{
		Path: "public/name.zip",
		Dir: "public",
	}).UnZip()
*/
func (this *FileStruct) UnZip() (result *FileResponse) {

	if Is.Empty(this.request.Dir) {
		this.response.Error = errors.New("解压路径不能为空")
		return this.response
	}

	if Is.Empty(this.request.Path) && !Is.Empty(this.request.Name) {

		// 判断 Dir 是否以 / 结尾
		if this.request.Dir[len(this.request.Dir)-1:] != "/" {
			this.request.Dir += "/"
		}

		// 判断 Name 是否以 .zip 结尾
		if this.request.Name[len(this.request.Name)-4:] != ".zip" {
			this.request.Name += ".zip"
		}

		this.request.Path = this.request.Dir + this.request.Name
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("压缩包路径不能为空")
		return this.response
	}

	// 判断压缩包是否存在
	if _, err := os.Stat(this.request.Path); os.IsNotExist(err) {
		this.response.Error = err
		return this.response
	}

	// 读取压缩包
	read, err := zip.OpenReader(this.request.Path)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	defer func(read *zip.ReadCloser) {
		err := read.Close()
		if err != nil {
			this.response.Error = err
			return
		}
	}(read)

	for _, file := range read.File {
		// 解压文件
		err := this.extract(file, this.request.Dir)
		if err != nil {
			this.response.Error = err
			return this.response
		}
	}

	this.response.Text = "1"
	this.response.Result = true
	this.response.Byte = []byte{1}

	return this.response
}

// 提取文件
func (this *FileStruct) extract(file *zip.File, dir string) (err error) {

	read, err := file.Open()
	if err != nil {
		return err
	}
	defer func(read io.ReadCloser) {
		err := read.Close()
		if err != nil {

		}
	}(read)

	path := filepath.Join(dir, file.Name)

	// 防止 ZipSlip：解压后的路径必须位于目标目录内
	if !strings.HasPrefix(path, filepath.Clean(dir)+string(os.PathSeparator)) {
		return errors.New("非法的压缩包路径: " + file.Name)
	}

	if file.FileInfo().IsDir() {

		err := os.MkdirAll(path, file.Mode())
		if err != nil {
			return err
		}

	} else {

		err := os.MkdirAll(filepath.Dir(path), os.ModePerm)
		if err != nil {
			return err
		}
		item, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())

		if err != nil {
			return err
		}
		defer func(item *os.File) {
			err := item.Close()
			if err != nil {
				return
			}
		}(item)

		_, err = io.Copy(item, read)
		if err != nil {
			return err
		}
	}

	return nil
}

// Rename 重命名文件
func (this *FileStruct) Rename(path ...any) (result *FileResponse) {

	if len(path) != 0 {
		this.request.Path = cast.ToString(path[0])
	}

	if Is.Empty(this.request.Path) {
		this.response.Error = errors.New("文件路径不能为空")
		return this.response
	}

	if Is.Empty(this.request.Name) {
		this.response.Error = errors.New("文件名不能为空")
		return this.response
	}

	// 判断文件是否存在
	if _, err := os.Stat(this.request.Path); os.IsNotExist(err) {
		this.response.Error = err
		return this.response
	}

	// 重命名文件 - 放在同一个目录下
	err := os.Rename(this.request.Path, filepath.Dir(this.request.Path)+"/"+this.request.Name)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	this.response.Text = "1"
	this.response.Result = true
	this.response.Byte = []byte{1}

	return this.response
}
