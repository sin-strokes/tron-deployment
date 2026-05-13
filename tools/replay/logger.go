package main

import (
	"encoding/json"
	"os"
)

// jsonlLogger 把每条记录序列化为一行 JSON 写入文件，append 模式。
//
// 故意保持简单（不带 buffer），方便用 tail -F 实时观察。
type jsonlLogger struct {
	f *os.File
}

func openJsonlLogger(path string) (*jsonlLogger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &jsonlLogger{f: f}, nil
}

func (l *jsonlLogger) writeRecord(rec map[string]any) {
	data, _ := json.Marshal(rec)
	_, _ = l.f.Write(data)
	_, _ = l.f.Write([]byte("\n"))
}

func (l *jsonlLogger) Close() { _ = l.f.Close() }
