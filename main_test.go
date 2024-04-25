package main

import (
	"testing"
)

func TestPathRe(t *testing.T) {
	t.Parallel()

	channelName, token, err := parsePath("/p/random/xxx/")
	if err != nil || channelName != "random" || token != "xxx" {
		t.Errorf("case 1 failed: %s", err)
	}
	if _, _, err := parsePath("/xxx/random"); err == nil {
		t.Error("case 2 failed")
	}
	if _, _, err := parsePath("/xxx/random/abc/"); err == nil {
		t.Error("case 3 failed")
	}
	if _, _, err := parsePath("/xxx/random/abc"); err == nil {
		t.Error("case 4 failed")
	}

	t.Run("URL decode", func(t *testing.T) {
		channelName, token, err := parsePath("/p/%E3%81%93%E3%82%93%E3%81%AB%E3%81%A1%E3%81%AF/xxxx")
		if err != nil || channelName != "こんにちは" || token != "xxxx" {
			t.Errorf("url encoded channel case failed: %s", err)
		}
	})
}
