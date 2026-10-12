package provider

import "context"

// PeekTitle is a source's one-line title, or empty when the source names none. A source names its title by
// implementing Title; the vendor decides what the title is and caches it, so an unchanged session is not
// read again.
func PeekTitle(ctx context.Context, src Source) string {
	if t, ok := src.(interface{ Title(context.Context) string }); ok {
		return t.Title(ctx)
	}
	return ""
}
