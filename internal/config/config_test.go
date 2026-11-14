package config

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"strings"
	"testing"
)

func TestParsePlainAndLenient(t *testing.T) {
	raw := []byte(`{
		// comment, URL below must survive
		"sites": [
			{"key": "a", "name": "甲", "type": 1, "api": "https://example.com/api.php/provide/vod/",},
			{"key": "jar", "name": "爬虫", "type": 3, "api": "csp_AppYS", "ext": ""},
		],
		"lives": [ [], {"name": "直播", "url": "https://example.com/live.m3u"}, {"nope": true} ],
		"parses": [ {"name": "json", "type": 1, "url": "https://parse.example/?url="} ],
	}`)
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 2 {
		t.Fatalf("sites = %d", len(cfg.Sites))
	}
	if cfg.Sites[0].API != "https://example.com/api.php/provide/vod/" {
		t.Fatalf("api = %s", cfg.Sites[0].API)
	}
	if len(cfg.Lives) != 1 || cfg.Lives[0].Name != "直播" {
		t.Fatalf("lives = %+v", cfg.Lives)
	}
	if len(cfg.Parses) != 1 || cfg.Parses[0].Type != 1 {
		t.Fatalf("parses = %+v", cfg.Parses)
	}
	jar := Classify(cfg.Sites[1])
	if jar.Kind != KindJar || jar.Level != LevelNo {
		t.Fatalf("jar classify = %+v", jar)
	}
	if !strings.Contains(jar.Reason, "不执行") && !strings.Contains(jar.Reason, "JAR") {
		t.Fatalf("reason = %s", jar.Reason)
	}
}

func TestParseLeadingComments(t *testing.T) {
	raw := []byte("// box.json\n// 语言：JSON\n/* block\n comment */\n{\n \"sites\":[\n  {\"key\":\"a\",\"name\":\"A\",\"type\":1,\"api\":\"https://example.com/api.php/provide/vod/\"}\n ],\n}")
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 1 || cfg.Sites[0].Key != "a" {
		t.Fatalf("sites = %+v", cfg.Sites)
	}
}

func TestParseBase64AndGzip(t *testing.T) {
	plain := []byte(`{"sites":[{"key":"x","name":"X","type":0,"api":"https://cms.example/api.php/provide/vod/"}]}`)
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	wrapped := []byte("abc**" + base64.StdEncoding.EncodeToString(gz.Bytes()))
	cfg, err := Parse(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 1 || cfg.Sites[0].Type != TypeXML {
		t.Fatalf("%+v", cfg.Sites)
	}
	cap := Classify(cfg.Sites[0])
	if cap.Kind != KindCMSXML || cap.Level != LevelYes {
		t.Fatalf("%+v", cap)
	}
}

func TestHexCipherIsNotBase64(t *testing.T) {
	raw := []byte("242377796378642324fc526118f0e7ec8fcc62f7db20fa08c23c4a3cdd091cfe")
	_, err := Parse(raw)
	if err == nil || !strings.Contains(err.Error(), "无法识别") {
		t.Fatal(err)
	}
}

func TestParseArrayAndIndex(t *testing.T) {
	raw := []byte(`[
		{"sites":[{"key":"a","name":"A","type":1,"api":"https://a.example/vod"}]},
		{"urls":[{"name":"仓","url":"./other.json"}], "storeHouse":[{"name":"仓2","url":"https://b.example/2.json"}]}
	]`)
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 1 {
		t.Fatalf("sites %d", len(cfg.Sites))
	}
	cfg.SourceURL = "https://example.com/index.json"
	cfg.ResolveRelatives()
	idx := cfg.IndexEntries()
	if len(idx) != 2 {
		t.Fatalf("index %+v", idx)
	}
	if idx[0].URL != "https://example.com/other.json" {
		t.Fatalf("rel = %s", idx[0].URL)
	}
}

func TestClassifyTable(t *testing.T) {
	cases := []struct {
		site Site
		kind string
		lv   Level
	}{
		{Site{Type: TypeJSON, API: "https://x/api.php/provide/vod/"}, KindCMSJSON, LevelYes},
		{Site{Type: TypeXML, API: "https://x/api.php/provide/vod/"}, KindCMSXML, LevelYes},
		{Site{Type: TypeXML, API: "xpath-rule"}, KindXPath, LevelNo},
		{Site{Type: TypeSpider, API: "csp_XBPQ", Ext: "https://x/r.json"}, KindXBPQ, LevelYes},
		{Site{Type: TypeSpider, API: "csp_XyqHiker", Ext: "https://x/r.json"}, KindXYQ, LevelPartial},
		{Site{Type: TypeSpider, API: "csp_AppYS"}, KindJar, LevelNo},
		{Site{Type: TypeSpider, API: "./lib/drpy2.min.js", Ext: "./js/a.js"}, KindJS, LevelPartial},
		{Site{Type: TypeSpider, API: "https://x/s.py"}, KindPython, LevelNo},
		{Site{Type: TypeSpider, API: "https://x/a.jar"}, KindJar, LevelNo},
		{Site{Type: TypeRemote, API: "https://x/cfg.json"}, KindRemote, LevelNo},
	}
	for _, tc := range cases {
		got := Classify(tc.site)
		if got.Kind != tc.kind || got.Level != tc.lv {
			t.Errorf("%+v -> %+v, want %s/%v", tc.site, got, tc.kind, tc.lv)
		}
	}
}

func TestParseStringNumbers(t *testing.T) {
	raw := []byte(`{"sites":[
		{"key":"a","name":"甲","type":"3","searchable":"1","quickSearch":true,"playerType":"2","api":"csp_XBPQ","ext":"./a.json"},
		"nope",
		{"key":"b","name":"乙","type":1,"api":"https://example.com/api.php/provide/vod/"}
	]}`)
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 2 {
		t.Fatalf("sites = %+v", cfg.Sites)
	}
	if cfg.Sites[0].Type != TypeSpider || cfg.Sites[0].PlayerType != 2 || cfg.Sites[0].Searchable != 1 || cfg.Sites[0].QuickSearch != 1 {
		t.Fatalf("%+v", cfg.Sites[0])
	}
	if Classify(cfg.Sites[0]).Kind != KindXBPQ {
		t.Fatalf("%+v", Classify(cfg.Sites[0]))
	}
}

func TestExtObject(t *testing.T) {
	raw := []byte(`{"sites":[{"key":"j","name":"J","type":3,"api":"./drpy2.min.js","ext":{"categories":"电影"}}]}`)
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.Sites[0].ExtString(), "categories") {
		t.Fatalf("ext = %s", cfg.Sites[0].ExtString())
	}
	if Classify(cfg.Sites[0]).Kind != KindJS {
		t.Fatalf("%+v", Classify(cfg.Sites[0]))
	}
}
