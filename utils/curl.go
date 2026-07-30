package utils

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cast"
)

// CurlRequest - 发起请求的结构体
type CurlRequest struct {
	Body    any
	Url     string
	Method  string
	Client  *http.Client
	Data    map[string]any
	Query   map[string]any
	Headers map[string]any
}

// CurlResponse - 响应的结构体
type CurlResponse struct {
	StatusCode int
	Request    *http.Request
	Headers    *http.Header
	Body       *io.ReadCloser
	Byte       []byte
	Text       string
	Json       map[string]any
	Error      error
}

// CurlClass - Curl 结构体
type CurlClass struct {
	request  *CurlRequest
	response *CurlResponse
}

// Curl - 发起请求 - 入口
func Curl(request ...CurlRequest) *CurlClass {

	if len(request) == 0 {
		request = append(request, CurlRequest{})
	}

	if Is.Empty(request[0].Method) {
		request[0].Method = "GET"
	}

	if Is.Empty(request[0].Data) {
		request[0].Data = make(map[string]any)
	}

	if Is.Empty(request[0].Query) {
		request[0].Query = make(map[string]any)
	}

	if Is.Empty(request[0].Headers) {
		request[0].Headers = make(map[string]any)
	}

	if Is.Empty(request[0].Client) {
		// 默认客户端带 30 秒超时，避免请求无限挂起（用户显式传入的 Client 不受影响）
		request[0].Client = &http.Client{ Timeout: 30 * time.Second }
	}

	return &CurlClass{
		request: &request[0],
		response: &CurlResponse{
			Json: make(map[string]any),
		},
	}
}

// Get - 发起 GET 请求
func (this *CurlClass) Get(url string) *CurlClass {
	this.request.Url = url
	this.request.Method = "GET"
	return this
}

// Post - 发起 POST 请求
func (this *CurlClass) Post(url string) *CurlClass {
	this.request.Url = url
	this.request.Method = "POST"
	return this
}

// Put - 发起 PUT 请求
func (this *CurlClass) Put(url string) *CurlClass {
	this.request.Url = url
	this.request.Method = "PUT"
	return this
}

// Patch - 发起 PATCH 请求
func (this *CurlClass) Patch(url string) *CurlClass {
	this.request.Url = url
	this.request.Method = "PATCH"
	return this
}

// Delete - 发起 DELETE 请求
func (this *CurlClass) Delete(url string) *CurlClass {
	this.request.Url = url
	this.request.Method = "DELETE"
	return this
}

// Method - 定义请求类型 - 默认 GET
func (this *CurlClass) Method(method string) *CurlClass {
	this.request.Method = strings.ToUpper(method)
	return this
}

// Url - 定义请求地址
func (this *CurlClass) Url(url string) *CurlClass {
	this.request.Url = url
	return this
}

// Header - 定义请求头
func (this *CurlClass) Header(key any, value any) *CurlClass {
	this.request.Headers[cast.ToString(key)] = cast.ToString(value)
	return this
}

// Headers - 批量定义请求头
func (this *CurlClass) Headers(headers map[string]any) *CurlClass {
	for key, val := range headers {
		this.request.Headers[cast.ToString(key)] = cast.ToString(val)
	}
	return this
}

// Query - 定义请求参数
func (this *CurlClass) Query(key any, value any) *CurlClass {
	this.request.Query[cast.ToString(key)] = cast.ToString(value)
	return this
}

// Querys - 批量定义请求参数
func (this *CurlClass) Querys(params map[string]any) *CurlClass {
	for key, val := range params {
		this.request.Query[cast.ToString(key)] = cast.ToString(val)
	}
	return this
}

// Data - 定义请求数据
func (this *CurlClass) Data(key string, value any) *CurlClass {
	this.request.Data[key] = cast.ToString(value)
	return this
}

// Datas - 批量定义请求数据
func (this *CurlClass) Datas(data map[string]any) *CurlClass {
	for key, val := range data {
		this.request.Data[key] = cast.ToString(val)
	}
	return this
}

// Body - 定义请求体
func (this *CurlClass) Body(body any) *CurlClass {
	this.request.Body = body
	return this
}

// Client - 定义请求客户端
func (this *CurlClass) Client(client *http.Client) *CurlClass {
	this.request.Client = client
	return this
}

// Send - 发起请求
func (this *CurlClass) Send() *CurlResponse {

	if Is.Empty(this.request.Url) {
		this.response.Error = errors.New("url is required")
		return this.response
	}

	// Encode query parameters if any
	if len(this.request.Query) > 0 {
		query := url.Values{}
		for key, val := range this.request.Query {
			query.Add(key, cast.ToString(val))
		}
		this.request.Url += "?" + query.Encode()
	}

	// 如果没有设置 Content-Type 则默认为 application/json
	if _, ok := this.request.Headers["Content-Type"]; !ok {
		this.request.Headers["Content-Type"] = "application/json"
	}

	// Create request object
	var buffer []byte
	contentType, ok := this.request.Headers["Content-Type"]
	if ok {
		switch {
		case strings.Contains(cast.ToString(contentType), "application/json"):

			// buffer, _ = json.Marshal(this.request.Body)

			// 如果 this.request.Body 是 map 类型，则直接转换为 json
			if Is.Map(this.request.Body) {
				buffer = []byte(Json.Encode(this.request.Body))
			} else {
				buffer = []byte(cast.ToString(this.request.Body))
			}

		case strings.Contains(cast.ToString(contentType), "application/x-www-form-urlencoded"):
			form := url.Values{}
			for key, val := range this.request.Data {
				form.Add(key, cast.ToString(val))
			}
			buffer = []byte(form.Encode())
		case strings.Contains(cast.ToString(contentType), "multipart/form-data"):
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			for key, val := range this.request.Data {
				err := writer.WriteField(key, cast.ToString(val))
				if err != nil {
					this.response.Error = err
					return this.response
				}
			}
			// add file field to request
			if file, ok := this.request.Body.(*multipart.FileHeader); ok {
				filePart, err := writer.CreateFormFile("file", file.Filename)
				if err != nil {
					this.response.Error = err
					return this.response
				}
				item, err := file.Open()
				if err != nil {
					this.response.Error = err
					return this.response
				}
				defer func(item multipart.File) {
					err := item.Close()
					if err != nil {
						this.response.Error = err
						return
					}
				}(item)
				_, err = io.Copy(filePart, item)
				if err != nil {
					this.response.Error = err
					return this.response
				}
			}
			err := writer.Close()
			if err != nil {
				this.response.Error = err
				return this.response
			}
			this.request.Headers["Content-Type"] = writer.FormDataContentType()
			buffer = body.Bytes()
		default:
			buffer = []byte(fmt.Sprintf("%v", this.request.Body))
		}
	}

	req, err := http.NewRequest(strings.ToUpper(this.request.Method), this.request.Url, bytes.NewBuffer(buffer))
	if err != nil {
		this.response.Error = err
		return this.response
	}

	for key, val := range this.request.Headers {
		req.Header.Set(key, cast.ToString(val))
	}

	// Make HTTP request
	response, err := this.request.Client.Do(req)
	if err != nil {
		this.response.Error = err
		return this.response
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			this.response.Error = err
			return
		}
	}(response.Body)

	// Read response body
	body, err := io.ReadAll(response.Body)
	if err != nil {
		this.response.Error = err
		return this.response
	}

	// Set response
	this.response.Byte = body
	this.response.Body = &response.Body
	this.response.Text = string(body)
	this.response.Headers = &response.Header
	this.response.Request = response.Request
	this.response.Json = cast.ToStringMap(Json.Decode(string(body)))
	this.response.StatusCode = response.StatusCode

	return this.response
}

// Redirect - 获取重定向地址
func Redirect(url any) (result string) {
	return redirect(cast.ToString(url), 0)
}

// redirect - 递归获取重定向地址，depth 限制最大重定向深度，避免循环重定向导致栈溢出
func redirect(url string, depth int) (result string) {

	// 超过最大重定向深度
	if depth >= 10 {
		return "maximum redirect depth exceeded"
	}

	item := Curl(CurlRequest{
		Method: "GET",
		Url:    url,
		Client: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}).Send()

	if item.Error != nil {
		result = item.Error.Error()
		return
	}

	if item.StatusCode == 301 || item.StatusCode == 302 {
		result = redirect(item.Headers.Get("Location"), depth+1)
		return
	}

	result = item.Request.URL.String()

	return
}
