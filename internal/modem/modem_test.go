package modem

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestUCS2AndSplit(t *testing.T) {
	if got := encodeUCS2("中文"); got != "4E2D6587" {
		t.Fatalf("ucs2=%s", got)
	}
	if got := normalizeDecoded("4E2D6587"); got != "中文" {
		t.Fatalf("decode=%s", got)
	}
	chunks := splitRunes("一二三四五", 2)
	if len(chunks) != 3 || chunks[2] != "五" {
		t.Fatalf("chunks=%#v", chunks)
	}
}

func TestModemInfoParsers(t *testing.T) {
	if got := quotedAt(`+CNUM: "","+8613800138000",145`, 1); got != "+8613800138000" {
		t.Fatalf("phone=%q", got)
	}
	if got := valueAfterColon("+ICCID: 89860123456789012345"); got != "89860123456789012345" {
		t.Fatalf("iccid=%q", got)
	}
	if got := valueAfterColon("+CGMR: LE30B01SIM7600M11_A"); got != "LE30B01SIM7600M11_A" {
		t.Fatalf("firmware=%q", got)
	}
	if got := firstValueLine("SIMCOM INCORPORATED\n"); got != "SIMCOM INCORPORATED" {
		t.Fatalf("manufacturer=%q", got)
	}
	if got := subVersion("+CSUB: B01V01\n+CSUB: MDM9x07_7600_11_A_V1.00_190927"); got != "B01V01 · MDM9x07_7600_11_A_V1.00_190927" {
		t.Fatalf("sub-version=%q", got)
	}
	if got := accessTechnology(`+COPS: 0,0,"CHN-UNICOM",7`); got != "LTE" {
		t.Fatalf("access technology=%q", got)
	}
	if got := accessTechnology(`+COPS: 0`); got != "" {
		t.Fatalf("malformed access technology=%q", got)
	}
}

func TestReadLoopEmitsSMSPromptWithoutNewline(t *testing.T) {
	controller := &ATController{
		lines:  make(chan string, 8),
		events: make(chan Event, 2),
	}
	controller.readLoop(context.Background(), bytes.NewBufferString("\r\n> \r\n+CMGS: 42\r\nOK\r\n"))
	want := []string{">", "+CMGS: 42", "OK"}
	for _, expected := range want {
		select {
		case got := <-controller.lines:
			if got != expected {
				t.Fatalf("line=%q want=%q", got, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing line %q", expected)
		}
	}
}
