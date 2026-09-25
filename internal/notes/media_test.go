package notes

import "testing"

func TestMediaRefsFindsEachPictureOnce(t *testing.T) {
	body := "Tafel:\n![](covey-media://11111111-2222-3333-4444-555555555555)\n" +
		"noch einmal ![x](covey-media://11111111-2222-3333-4444-555555555555) und " +
		"![](covey-media://aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee) — kein Bild: covey-media://nope"
	got := MediaRefs(body)
	if len(got) != 2 || got[0].String() != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("two distinct references expected: %v", got)
	}
}
