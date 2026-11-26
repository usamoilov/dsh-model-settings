package i18n

import (
	"regexp"
	"sort"
	"testing"
)

func TestDictionaries(t *testing.T) {
	ph := regexp.MustCompile(`\{\w+\}`)
	for locale, dict := range Messages {
		for key, text := range en {
			other, ok := dict[key]
			if !ok {
				t.Errorf("%s: missing %s", locale, key)
				continue
			}
			a, b := ph.FindAllString(text, -1), ph.FindAllString(other, -1)
			sort.Strings(a)
			sort.Strings(b)
			if len(a) != len(b) {
				t.Errorf("%s: %s placeholders %v vs %v", locale, key, b, a)
				continue
			}
			for i := range a {
				if a[i] != b[i] {
					t.Errorf("%s: %s placeholders %v vs %v", locale, key, b, a)
				}
			}
		}
		for key := range dict {
			if _, ok := en[key]; !ok {
				t.Errorf("%s: extra key %s", locale, key)
			}
		}
	}
}

func TestTranslator(t *testing.T) {
	cases := map[string]string{"ru": "ru", "zh-CN": "zh", "de": "en", "": "en", "RU_ru": "ru"}
	for in, want := range cases {
		if got := LocaleOf(in); got != want {
			t.Errorf("LocaleOf(%q) = %q", in, got)
		}
	}
	if got := Translator("en")("err.notInCatalog", Params{"model": "p / m"}); got != "The catalog has no model p / m" {
		t.Error(got)
	}
	if got := Translator("ru")("auto", nil); got != "Авто" {
		t.Error(got)
	}
	if got := Translator("xx")("auto", nil); got != "Auto" {
		t.Error(got)
	}
	if got := English("no.such.key", nil); got != "no.such.key" {
		t.Error(got)
	}
	if got := English("err.search", Params{}); got != "Search failed: {error}" {
		t.Error("a missing parameter stays visible:", got)
	}
	f := Fail(422, "err.compose", Params{"target": "Profile web", "problem": "x"})
	if f.Status != 422 || f.Key != "err.compose" || f.Error() != "Profile web: x. Changes were rolled back." {
		t.Error(f.Status, f.Key, f.Error())
	}
	what := &Failure{Status: 400, Key: "err.noRef", WhatKey: "what.subagent"}
	if got := what.Message(Translator("ru")); got != "Не выбрана модель сабагентов" {
		t.Error(got)
	}
}
