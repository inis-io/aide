package utils

import (
	"github.com/spf13/cast"
	"golang.org/x/crypto/bcrypt"
)

// Password - 密码
var Password *PasswordStruct

type PasswordStruct struct{}

// Create - 创建密码
func (this *PasswordStruct) Create(password any) (result string) {

	item, _ := bcrypt.GenerateFromPassword([]byte(cast.ToString(password)), bcrypt.DefaultCost)

	return string(item)
}

// Verify - 验证密码
func (this *PasswordStruct) Verify(encode any, password any) (ok bool) {
	if err := bcrypt.CompareHashAndPassword([]byte(cast.ToString(encode)), []byte(cast.ToString(password))); err != nil {
		return false
	}
	return true
}
