package main

import (
	"fmt"
	"strings"
)

// The IR's functional source is a Markdown contract, not another IR document.
// Keep its referenced ownership/audit chapters and implementation history
// addressable: existence and matching issue-ID substrings alone are not enough.
func validateFunctionalContract(data []byte) error {
	text := strings.TrimSpace(string(data))
	if !strings.HasPrefix(text, "# Vortex 功能模拟器 Living Architecture Contract\n") {
		return fmt.Errorf("functional contract: expected Living Architecture Markdown title")
	}
	headings := map[int]bool{}
	for _, line := range strings.Split(text, "\n") {
		for section := 1; section <= 16; section++ {
			if strings.HasPrefix(line, fmt.Sprintf("## %d. ", section)) {
				headings[section] = true
			}
		}
	}
	for section := 1; section <= 16; section++ {
		if !headings[section] {
			return fmt.Errorf("functional contract: missing section %d", section)
		}
	}
	return nil
}
