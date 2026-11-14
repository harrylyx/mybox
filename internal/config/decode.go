package config

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
)

const (
	maxDecodeDepth = 8
	maxDecodedSize = 32 << 20
)

// Decode unwraps the wrappers commonly seen on TVBox subscriptions:
// gzip, a short prefix before "**", and standard base64. Plain JSON is
// returned as-is. Nested wrappers are peeled up to a small depth.
func Decode(raw []byte) ([]byte, error) {
	return decode(raw, 0)
}

func decode(raw []byte, depth int) ([]byte, error) {
	if depth > maxDecodeDepth {
		return nil, fmt.Errorf("编码嵌套超过 %d 层", maxDecodeDepth)
	}
	raw = bytes.TrimSpace(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}))
	if len(raw) == 0 {
		return nil, fmt.Errorf("配置内容为空")
	}
	if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		out, err := gunzip(raw)
		if err != nil {
			return nil, err
		}
		return decode(out, depth+1)
	}
	if raw[0] == '{' || raw[0] == '[' {
		return raw, nil
	}
	// JSONC subscriptions open with // or /* comments, hiding the opening
	// brace from the check above. Lenient strips comments (and is a no-op
	// on the already-cleaned output Parse feeds it later); accept the
	// result when the document now starts like JSON.
	if raw[0] == '/' {
		if cleaned := bytes.TrimSpace(Lenient(raw)); len(cleaned) > 0 && (cleaned[0] == '{' || cleaned[0] == '[') {
			return cleaned, nil
		}
	}
	if i := bytes.Index(raw, []byte("**")); i >= 0 && i < 64 {
		if out, ok := tryBase64(raw[i+2:]); ok {
			return decode(out, depth+1)
		}
	}
	if out, ok := tryBase64(raw); ok {
		return decode(out, depth+1)
	}
	return nil, fmt.Errorf("无法识别的配置编码，开头为 %q", raw[:min(32, len(raw))])
}

func gunzip(raw []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("gzip 解压失败: %w", err)
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, maxDecodedSize+1))
	if err != nil {
		return nil, fmt.Errorf("gzip 读取失败: %w", err)
	}
	if len(out) > maxDecodedSize {
		return nil, fmt.Errorf("解码后内容超过 %d 字节", maxDecodedSize)
	}
	return out, nil
}

func tryBase64(b []byte) ([]byte, bool) {
	s := stringsTrimSpaceAll(b)
	if len(s) < 16 {
		return nil, false
	}
	if pad := len(s) % 4; pad != 0 {
		s += string(bytes.Repeat([]byte("="), 4-pad))
	}
	out, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(out) == 0 {
		out, err = base64.RawStdEncoding.DecodeString(stringsTrimSpaceAll(b))
		if err != nil || len(out) == 0 {
			return nil, false
		}
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, false
	}
	// JSON, gzip, or another "prefix**base64" wrapper. A `{` buried in
	// decoded noise is not enough: hex ciphertext is valid base64 alphabet
	// and would otherwise be peeled into garbage.
	if out[0] == '{' || out[0] == '[' || (len(out) > 2 && out[0] == 0x1f && out[1] == 0x8b) {
		return out, true
	}
	if mostlyText(out) {
		if i := bytes.Index(out, []byte("**")); i >= 0 && i < 64 {
			return out, true
		}
	}
	return nil, false
}

func stringsTrimSpaceAll(b []byte) string {
	var buf bytes.Buffer
	for _, c := range b {
		if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
			continue
		}
		buf.WriteByte(c)
	}
	return buf.String()
}

func mostlyText(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	n := 0
	for _, c := range b {
		if c == '\n' || c == '\r' || c == '\t' || (c >= 0x20 && c < 0x7f) || c >= 0x80 {
			n++
		}
	}
	return n*5 >= len(b)*4
}
