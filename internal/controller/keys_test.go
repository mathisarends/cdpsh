package controller

import "testing"

func TestShiftedDigitsAndShortcuts(t *testing.T) {
	for i, want := range ")!@#$%^&*(" {
		digit := string(rune('0' + i))
		for _, modifier := range []string{"", "Shift+", "Ctrl+Shift+"} {
			k, mod, err := KeySpec(modifier + digit)
			if err != nil {
				t.Fatal(err)
			}
			key, text, modifiers := digit, digit, 0
			if modifier != "" {
				key, text, modifiers = string(want), string(want), 8
			}
			if modifier == "Ctrl+Shift+" {
				text, modifiers = "", 10
			}
			if k.Key != key || k.Text != text || k.Code != "Digit"+digit || k.VK != '0'+i || mod != modifiers {
				t.Fatalf("%s%s: key=%+v modifiers=%d", modifier, digit, k, mod)
			}
		}
	}
}
