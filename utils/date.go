package utils

import (
	"fmt"
	"time"
	
	"github.com/spf13/cast"
)

var Date *DateClass

func init() {
	// 初始化日期实例，默认当前时间
	Date = &DateClass{ Time: time.Now() }
}

type DateClass struct {
	Time time.Time
}

// Format 格式化日期
func (this *DateClass) Format(layout string) string {
	return this.Time.Format(layout)
}

// FormatSeconds 将秒数格式化为友好的时间字符串
func (this *DateClass) FormatSeconds(seconds any) string {
	
	value := cast.ToInt(seconds)
	
	if value < 60 {
		return fmt.Sprintf("%d秒", value)
	}
	
	if value < 3600 {
		
		minutes := value / 60
		remain  := value % 60
		
		if remain == 0 {
			return fmt.Sprintf("%d分钟", minutes)
		}
		if remain < 10 {
			return fmt.Sprintf("%d分钟0%d秒", minutes, remain)
		}
		return fmt.Sprintf("%d分钟%d秒", minutes, remain)
	}
	
	if value < 86400 {
		
		hours   := value / 3600
		remain  := value % 3600
		minutes := remain  / 60
		remain   = remain % 60
		
		result := fmt.Sprintf("%d小时", hours)
		
		if minutes > 0 {
			result += fmt.Sprintf("%d分钟", minutes)
		}
		
		if remain > 0 {
			if remain < 10 {
				result += fmt.Sprintf("0%d秒", remain)
			} else {
				result += fmt.Sprintf("%d秒", remain)
			}
		}
		
		return result
	}
	
	days   := value / 86400
	remain := value % 86400
	hours  := remain  / 3600
	
	remain   = remain % 3600
	minutes := remain / 60
	
	remain  = remain % 60
	result := fmt.Sprintf("%d天", days)
	
	if hours > 0 {
		result += fmt.Sprintf("%d小时", hours)
	}
	
	if minutes > 0 {
		result += fmt.Sprintf("%d分钟", minutes)
	}
	
	if remain > 0 {
		if remain < 10 {
			result += fmt.Sprintf("0%d秒", remain)
		} else {
			result += fmt.Sprintf("%d秒", remain)
		}
	}
	
	return result
}

// Today - 获取今天开始和结束时间（闭区间）
func (this *DateClass) Today(location any) (start time.Duration, end time.Duration) {
	
	if Is.Empty(location) { location = "Asia/Shanghai" }
	
	loc, err := time.LoadLocation(cast.ToString(location))
	// 非法时区回退为本地时区，避免 In(nil) 崩溃
	if err != nil { loc = time.Local }
	now    := time.Now().In(loc)
	today  := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	
	start = today.Sub(time.Unix(0, 0))
	end   = today.Add(24*time.Hour).Add(-time.Nanosecond).Sub(time.Unix(0, 0))
	return
}

// Yesterday - 获取昨天开始和结束时间（闭区间）
func (this *DateClass) Yesterday(location any) (start time.Duration, end time.Duration) {
	
	if Is.Empty(location) { location = "Asia/Shanghai" }
	
	loc, err := time.LoadLocation(cast.ToString(location))
	// 非法时区回退为本地时区，避免 In(nil) 崩溃
	if err != nil { loc = time.Local }
	now    := time.Now().In(loc)
	today  := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	
	start = today.Add(-24 * time.Hour).Sub(time.Unix(0, 0))
	end   = today.Add(-time.Nanosecond).Sub(time.Unix(0, 0))
	return
}

// Week - 获取本周开始和结束时间（闭区间）
func (this *DateClass) Week(location any) (start time.Duration, end time.Duration) {
	
	if Is.Empty(location) { location = "Asia/Shanghai" }
	
	loc, err := time.LoadLocation(cast.ToString(location))
	// 非法时区回退为本地时区，避免 In(nil) 崩溃
	if err != nil { loc = time.Local }
	now   := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	
	// 以周一作为每周起始（ISO习惯）
	offset := (int(today.Weekday()) + 6) % 7
	week   := today.AddDate(0, 0, -offset)
	
	start = week.Sub(time.Unix(0, 0))
	end   = week.AddDate(0, 0, 7).Add(-time.Nanosecond).Sub(time.Unix(0, 0))
	return
}

// LastWeek - 获取上周开始和结束时间（闭区间）
func (this *DateClass) LastWeek(location any) (start time.Duration, end time.Duration) {
	
	if Is.Empty(location) { location = "Asia/Shanghai" }
	
	loc, err := time.LoadLocation(cast.ToString(location))
	// 非法时区回退为本地时区，避免 In(nil) 崩溃
	if err != nil { loc = time.Local }
	now   := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	
	// 以周一作为每周起始（ISO习惯）
	offset := (int(today.Weekday()) + 6) % 7
	week   := today.AddDate(0, 0, -offset)
	
	start = week.AddDate(0, 0, -7).Sub(time.Unix(0, 0))
	end   = week.Add(-time.Nanosecond).Sub(time.Unix(0, 0))
	return
}

// Month - 获取本月开始和结束时间（闭区间）
func (this *DateClass) Month(location any) (start time.Duration, end time.Duration) {
	
	if Is.Empty(location) { location = "Asia/Shanghai" }
	
	loc, err := time.LoadLocation(cast.ToString(location))
	// 非法时区回退为本地时区，避免 In(nil) 崩溃
	if err != nil { loc = time.Local }
	now   := time.Now().In(loc)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	
	start = month.Sub(time.Unix(0, 0))
	end   = month.AddDate(0, 1, 0).Add(-time.Nanosecond).Sub(time.Unix(0, 0))
	return
}

// LastMonth - 获取上月开始和结束时间（闭区间）
func (this *DateClass) LastMonth(location any) (start time.Duration, end time.Duration) {
	
	if Is.Empty(location) { location = "Asia/Shanghai" }
	
	loc, err := time.LoadLocation(cast.ToString(location))
	// 非法时区回退为本地时区，避免 In(nil) 崩溃
	if err != nil { loc = time.Local }
	now   := time.Now().In(loc)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	
	start = month.AddDate(0, -1, 0).Sub(time.Unix(0, 0))
	end   = month.Add(-time.Nanosecond).Sub(time.Unix(0, 0))
	return
}

// Year - 获取今年开始和结束时间（闭区间）
func (this *DateClass) Year(location any) (start time.Duration, end time.Duration) {
	
	if Is.Empty(location) { location = "Asia/Shanghai" }
	
	loc, err := time.LoadLocation(cast.ToString(location))
	// 非法时区回退为本地时区，避免 In(nil) 崩溃
	if err != nil { loc = time.Local }
	now  := time.Now().In(loc)
	year := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc)
	
	start = year.Sub(time.Unix(0, 0))
	end   = year.AddDate(1, 0, 0).Add(-time.Nanosecond).Sub(time.Unix(0, 0))
	return
}

// LastYear - 获取去年开始和结束时间（闭区间）
func (this *DateClass) LastYear(location any) (start time.Duration, end time.Duration) {
	
	if Is.Empty(location) { location = "Asia/Shanghai" }
	
	loc, err := time.LoadLocation(cast.ToString(location))
	// 非法时区回退为本地时区，避免 In(nil) 崩溃
	if err != nil { loc = time.Local }
	now  := time.Now().In(loc)
	year := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, loc)
	
	start = year.AddDate(-1, 0, 0).Sub(time.Unix(0, 0))
	end   = year.Add(-time.Nanosecond).Sub(time.Unix(0, 0))
	return
}