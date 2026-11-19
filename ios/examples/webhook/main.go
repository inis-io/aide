// Command webhook - ios 事件通知 Webhook 接收器 demo（设计方案 §13 P3）
//
// 演示接入方接收平台对象事件的标准姿势：先验签 → 按投递号幂等 → 落库（此处以日志代替）→ 应答 2xx。
//
// 注册规则（一次性，用 SDK 完成）：
//
//	client.PutBucketNotification(ctx, "photos", []ios.NotificationRule{{
//	    Topic:  "https://你的公网地址/ios/webhook",
//	    Events: []string{ios.EventObjectCreated, ios.EventObjectRemoved},
//	}})
//	// Secret 留空由服务端生成，回读取真实值：
//	rules, _ := client.GetBucketNotification(ctx, "photos")
//	secret := rules[0].Secret
//
// 用法（Windows PowerShell）：
//
//	$env:IOS_WEBHOOK_SECRET="上一步回读的 Secret"
//	go run ./examples/webhook
//
// 可选环境变量：
//
//	IOS_WEBHOOK_ADDR - 监听地址（默认 :9100）
//
// 接入纪律（与服务端 at-least-once 投递语义配套，验签无跳过开关）：
//  1. 先验签再处理：验签失败一律 4xx，绝不解析载荷；
//  2. 按 X-Inis-Delivery 幂等：同一逻辑投递会重试（1s / 4s 退避，最多 3 次），重复投递直接 2xx 跳过；
//  3. 先落库再 2xx：2xx 即视为成功不再重试，务必先完成业务持久化。
package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/inis-io/aide/ios"
)

// receiver - 接收器状态（投递号幂等簿记）
type receiver struct {
	// secret - 验签密钥（规则配置回读的 Secret）
	secret string
	// mu - 幂等表锁
	mu sync.Mutex
	// seen - 已处理投递号（demo 用内存表；生产应落库 / Redis 并设保留期）
	seen map[string]struct{}
}

// handle - Webhook 入口（POST /ios/webhook）
func (this *receiver) handle(writer http.ResponseWriter, request *http.Request) {

	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// ① 原文读体（验签基于原始字节，不能先格式化）
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// ② 先验签：失败一律 400（不区分缺头 / 签名不符，避免泄露判定细节）
	event, err := ios.ParseEvent(body, request.Header, this.secret)
	if err != nil {
		log.Printf("验签失败：%v", err)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}

	// ③ 按投递号幂等（重复投递直接 2xx 跳过）
	this.mu.Lock()
	if _, ok := this.seen[event.Delivery]; ok {
		this.mu.Unlock()
		log.Printf("重复投递 %s（事件 %s），幂等跳过", event.Delivery, event.Name)
		writer.WriteHeader(http.StatusOK)
		return
	}
	this.seen[event.Delivery] = struct{}{}
	this.mu.Unlock()

	// ④ 业务处理（demo 打日志；生产先落库再应答）
	for _, record := range event.Records {
		log.Printf("事件 %s：桶 %s 对象 %s（%d 字节，ETag %s，时间 %s）",
			record.EventName, record.S3.Bucket.Name, record.S3.Object.Key,
			record.S3.Object.Size, record.S3.Object.ETag, record.EventTime)
	}

	// ⑤ 2xx 即成功，服务端不再重试
	writer.WriteHeader(http.StatusOK)
}

// main - 装配并启动接收器
func main() {

	log.SetFlags(0)

	secret := os.Getenv("IOS_WEBHOOK_SECRET")
	if secret == "" {
		log.Fatal("请先设置环境变量 IOS_WEBHOOK_SECRET（PutBucketNotification 后经 GetBucketNotification 回读）")
	}
	addr := os.Getenv("IOS_WEBHOOK_ADDR")
	if addr == "" {
		addr = ":9100"
	}

	handler := &receiver{secret: secret, seen: map[string]struct{}{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/ios/webhook", handler.handle)

	log.Printf("ios webhook 接收器监听 %s（POST /ios/webhook）", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
