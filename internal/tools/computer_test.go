package tools

import (
	"runtime"
	"strings"
	"testing"
)

func TestMouseCommandLinux(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("linux command test")
	}
	cmd := mouseCommand("click", 100, 200)
	joined := strings.Join(cmd, " ")
	if !strings.HasPrefix(joined, "xdotool mousemove 100 200") || !strings.Contains(joined, "click 1") {
		t.Fatalf("bad click command: %v", cmd)
	}
	if !strings.Contains(strings.Join(mouseCommand("right_click", 1, 2), " "), "click 3") {
		t.Fatal("right click should use button 3")
	}
	if !strings.Contains(strings.Join(mouseCommand("double_click", 1, 2), " "), "--repeat 2") {
		t.Fatal("double click should repeat")
	}
}

func TestTypeAndScrollCommands(t *testing.T) {
	if runtime.GOOS != "darwin" {
		if got := strings.Join(typeCommand("hi"), " "); !strings.Contains(got, "xdotool type") {
			t.Fatalf("type command: %s", got)
		}
		// negative scroll = up = button 4
		if !strings.Contains(strings.Join(scrollCommand(-2), " "), "4") {
			t.Fatal("scroll up should use button 4")
		}
		if !strings.Contains(strings.Join(scrollCommand(3), " "), "5") {
			t.Fatal("scroll down should use button 5")
		}
	}
}

func TestComputerDisabledByDefault(t *testing.T) {
	// With no config the tool refuses rather than acting.
	res := computerTool{}.Execute(nil, Input{Args: []byte(`{"action":"screenshot"}`)})
	if !res.IsError || !strings.Contains(res.Content, "off") {
		t.Fatalf("computer tool should be off by default, got %q", res.Content)
	}
}
