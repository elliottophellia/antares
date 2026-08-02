package rag

import "testing"

func TestSocialCollection(t *testing.T) {
	cases := []struct{ input, want string }{
		{"instagram", "social-instagram"},
		{"facebook", "social-facebook"},
		{"threads", "social-threads"},
		{"x", "social-x"},
		{"social/shared", "social-social-shared"},
		{"", "social-shared"},
		{"TikTok", "social-tiktok"},
	}
	for _, c := range cases {
		got := SocialCollection(c.input)
		if got != c.want {
			t.Errorf("SocialCollection(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
