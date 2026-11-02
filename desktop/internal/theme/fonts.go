package theme

import (
	"embed"
	"fmt"

	"github.com/egoist/mygo/ui"
)

// Mono is the code font: the bundled JetBrains Mono (SIL OFL 1.1, license
// beside it), then the system's monospace. Use it with FontFeatures
// "liga=0": code shows the characters typed.
const Mono = "JetBrains Mono, monospace"

//go:embed fonts/*.ttf
var fonts embed.FS

// RegisterFonts registers the bundled fonts; call it once before the first
// frame.
func RegisterFonts() error {
	for _, name := range []string{"JetBrainsMono-Regular.ttf", "JetBrainsMono-SemiBold.ttf"} {
		data, err := fonts.ReadFile("fonts/" + name)
		if err != nil {
			return fmt.Errorf("theme: %w", err)
		}
		if err := ui.RegisterFont(data, ""); err != nil {
			return fmt.Errorf("theme: register %s: %w", name, err)
		}
	}
	return nil
}
