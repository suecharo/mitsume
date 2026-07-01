package main

import "testing"

func TestRun_NoArgsExits1(t *testing.T) {
	if code := run(nil); code != 1 {
		t.Fatalf("run(nil) = %d, want 1", code)
	}
}

func TestRun_HelpFlagsExit0(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "help"} {
		t.Run(arg, func(t *testing.T) {
			if code := run([]string{arg}); code != 0 {
				t.Fatalf("run(%q) = %d, want 0", arg, code)
			}
		})
	}
}

func TestRun_UnknownSubcommandExits1(t *testing.T) {
	if code := run([]string{"nonexistent"}); code != 1 {
		t.Fatalf("run(nonexistent) = %d, want 1", code)
	}
}

func TestRun_UnimplementedSubcommandsExit2(t *testing.T) {
	for _, sub := range []string{"check", "watch", "run"} {
		t.Run(sub, func(t *testing.T) {
			if code := run([]string{sub}); code != 2 {
				t.Fatalf("run(%q) = %d, want 2", sub, code)
			}
		})
	}
}
