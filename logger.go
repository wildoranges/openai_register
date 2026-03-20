package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type LogLevel int

const (
	LOG_DEBUG LogLevel = iota
	LOG_INFO
	LOG_WARN
	LOG_ERROR
)

type Logger struct {
	showFile   bool
	showLine   bool
	timeFormat string
}

var (
	DefaultLogger = &Logger{
		showFile:   true,
		showLine:   true,
		timeFormat: "2006-01-02 15:04:05",
	}
	globalDebug bool
)

func getCallerInfo(skip int) (filename string, line int) {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "???", 0
	}

	filename = filepath.Base(file)
	return filename, line
}

func (l *Logger) formatLogPrefix() string {
	filename, line := getCallerInfo(3)

	timestamp := time.Now().Format(l.timeFormat)

	if l.showFile && l.showLine {
		return fmt.Sprintf("[%s] [%s:%d] ", timestamp, filename, line)
	} else if l.showFile {
		return fmt.Sprintf("[%s] [%s] ", timestamp, filename)
	}
	return fmt.Sprintf("[%s] ", timestamp)
}

func SetDebug(enabled bool) {
	globalDebug = enabled
}

func Print(a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Print(prefix)
	fmt.Println(a...)
}

func Printf(format string, a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Printf(prefix+format, a...)
}

func Println(a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Print(prefix)
	fmt.Println(a...)
}

func Fatal(a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Print(prefix)
	fmt.Println(a...)
	os.Exit(1)
}

func Fatalf(format string, a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Printf(prefix+format, a...)
	os.Exit(1)
}

func Debug(a ...interface{}) {
	if globalDebug {
		prefix := DefaultLogger.formatLogPrefix()
		fmt.Print(prefix)
		fmt.Println(a...)
	}
}

func Debugf(format string, a ...interface{}) {
	if globalDebug {
		prefix := DefaultLogger.formatLogPrefix()
		fmt.Printf(prefix+format, a...)
	}
}

func Info(a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Print(prefix)
	fmt.Println(a...)
}

func Infof(format string, a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Printf(prefix+format, a...)
}

func Warn(a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Print(prefix)
	fmt.Print("⚠️  ")
	fmt.Println(a...)
}

func Warnf(format string, a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Printf(prefix+"⚠️  "+format, a...)
}

func Error(a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Print(prefix)
	fmt.Print("❌ ")
	fmt.Println(a...)
}

func Errorf(format string, a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Printf(prefix+"❌ "+format, a...)
}

func Success(a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Print(prefix)
	fmt.Print("✅ ")
	fmt.Println(a...)
}

func Successf(format string, a ...interface{}) {
	prefix := DefaultLogger.formatLogPrefix()
	fmt.Printf(prefix+"✅ "+format, a...)
}

func Separator(title string) {
	if title != "" {
		fmt.Printf("\n%s %s %s\n", strings.Repeat("=", 20), title, strings.Repeat("=", 20))
	} else {
		fmt.Printf("%s\n", strings.Repeat("=", 60))
	}
}
