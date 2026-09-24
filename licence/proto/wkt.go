//go:build ignore

// wkt.go - 导出 protoc 需要的 Well-Known Types 描述符集（FileDescriptorSet）。
//
// 背景：apis/v1/runtime.proto import google/protobuf/struct.proto，protoc 必须能解析该导入。
// 官方 protoc 发行包自带 include/ 目录（把 include 目录加进 --proto_path 即可），但只拷了
// protoc.exe 的环境没有该目录。本工具从 module 已依赖的 google.golang.org/protobuf 运行时
// 导出官方描述符，供 protoc 的 --descriptor_set_in 使用——既避免在仓库里复制 Google 的
// .proto 源文件（单一权威来源），也不需要联网。
//
// 用法（工作目录 = licence/proto，见 generate.ps1）：
//
//	go run wkt.go "$env:TEMP\licence-wkt.pb"
//	protoc --descriptor_set_in="$env:TEMP\licence-wkt.pb" --proto_path=. `
//	  --go_out=. --go_opt=paths=source_relative `
//	  --go-grpc_out=. --go-grpc_opt=paths=source_relative apis/v1/runtime.proto
package main

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	_ "google.golang.org/protobuf/types/known/structpb"
)

// wktFiles - 需要导出的 Well-Known Types 契约（新增导入时在此登记）。
var wktFiles = []string{"google/protobuf/struct.proto"}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法: go run wkt.go <输出文件>.pb")
		os.Exit(2)
	}
	set := &descriptorpb.FileDescriptorSet{}
	for _, path := range wktFiles {
		file, err := protoregistry.GlobalFiles.FindFileByPath(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WKT 描述符未注册：%s（%v）\n", path, err)
			os.Exit(1)
		}
		set.File = append(set.File, protodesc.ToFileDescriptorProto(file))
	}
	raw, err := proto.Marshal(set)
	if err != nil {
		fmt.Fprintf(os.Stderr, "序列化 WKT 描述符集失败：%v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[1], raw, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "写入 WKT 描述符集失败：%v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已导出 %d 个 WKT 描述符到 %s\n", len(set.File), os.Args[1])
}
