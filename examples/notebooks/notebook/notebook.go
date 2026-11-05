// Package notebook prints notebook-style cells for the runnable examples.
package notebook

import (
	"fmt"
	"os"
	"strings"
)

// Cell prints a titled block of explanatory text before a code cell runs.
func Cell(title, text string) {
	fmt.Printf("\n─── %s ─────────────────────────────────────────\n%s\n\n", title, strings.TrimSpace(text))
}

// Result prints the output of a cell.
func Result(format string, args ...any) {
	fmt.Printf("→ "+format+"\n", args...)
}

// Skip prints a setup hint and reports whether the example can run.
func NeedEnv(names ...string) bool {
	var missing []string
	for _, name := range names {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return true
	}
	fmt.Printf("Пропущено: задайте %s и запустите снова.\n", strings.Join(missing, ", "))
	return false
}
