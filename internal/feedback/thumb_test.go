package feedback

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func pngOf(t *testing.T, w, h int, c color.Color) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func decodeThumb(t *testing.T, a Attachment) image.Image {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(a.DataBase64)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestThumbnailsShrinkKeepOrderAndKeepASlotForWhatTheyCannotRead(t *testing.T) {
	in := []Attachment{
		{MimeType: "image/png", DataBase64: pngOf(t, 800, 400, color.NRGBA{200, 30, 30, 255}), Name: "wide.png"},
		{MimeType: "image/png", DataBase64: "not base64!!", Name: "broken.png"},
		{MimeType: "image/png", DataBase64: pngOf(t, 100, 300, color.NRGBA{0, 0, 255, 255}), Name: "tall.png"},
		{MimeType: "image/png", DataBase64: pngOf(t, 40, 40, color.NRGBA{0, 0, 0, 0}), Name: "clear.png"},
	}
	out := Thumbnails(in)
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d: a slot must stay in its place", len(out), len(in))
	}
	if b := decodeThumb(t, out[0]).Bounds(); b.Dx() != thumbEdge || b.Dy() != thumbEdge/2 {
		t.Errorf("wide = %v, want %dx%d", b, thumbEdge, thumbEdge/2)
	}
	if out[1].DataBase64 != "" || out[1].Name != "broken.png" {
		t.Errorf("an unreadable image must keep its name and carry no bytes: %+v", out[1])
	}
	if b := decodeThumb(t, out[2]).Bounds(); b.Dy() != thumbEdge || b.Dx() >= thumbEdge {
		t.Errorf("tall = %v", b)
	}
	if r, g, bl, _ := decodeThumb(t, out[3]).At(5, 5).RGBA(); r>>8 < 240 || g>>8 < 240 || bl>>8 < 240 {
		t.Errorf("a transparent PNG must land on white, got %d %d %d", r>>8, g>>8, bl>>8)
	}
	if out[0].MimeType != "image/jpeg" {
		t.Errorf("mime = %q", out[0].MimeType)
	}
	if small := Thumbnails([]Attachment{{DataBase64: pngOf(t, 20, 10, color.NRGBA{9, 9, 9, 255})}}); decodeThumb(t, small[0]).Bounds().Dx() != 20 {
		t.Error("a small image is never scaled up")
	}
}

func TestThumbnailsRefuseADecompressionBomb(t *testing.T) {
	// 7000x7000 = 49 MP of one colour compresses to a few KB of PNG.
	big := Thumbnails([]Attachment{{DataBase64: pngOf(t, 7000, 7000, color.NRGBA{1, 2, 3, 255})}})
	if big[0].DataBase64 != "" {
		t.Error("an image past the pixel budget must not be decoded")
	}
}

func TestBrokerServesThumbnailsOfAnAnsweredAsk(t *testing.T) {
	s := NewMemory()
	ask, _, err := s.Create(CreateInput{ProjectRoot: t.TempDir(), Questions: []Question{{ID: "q", Text: "Look?", Type: "text"}}})
	if err != nil {
		t.Fatal(err)
	}
	big := pngOf(t, 640, 480, color.NRGBA{10, 120, 200, 255})
	if err := s.Resolve(ask.ID, map[string]Answer{"q": {Custom: "see"}}, []Attachment{{MimeType: "image/png", DataBase64: big, Name: "a.png"}}, nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewHandler(&Broker{Store: s, Token: "tok-tok-tok"}))
	defer srv.Close()
	get := func(id string) []Attachment {
		req, _ := http.NewRequest("GET", srv.URL+"/v1/inbox/asks/"+id+"/thumbnails", nil)
		req.Header.Set("Authorization", "Bearer tok-tok-tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			Thumbnails []Attachment `json:"thumbnails"`
		}
		if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&out) != nil || out.Thumbnails == nil {
			t.Fatalf("status %d, thumbnails %v", resp.StatusCode, out.Thumbnails)
		}
		return out.Thumbnails
	}
	got := get(ask.ID)
	if len(got) != 1 || got[0].Name != "a.png" || len(got[0].DataBase64) >= len(big) {
		t.Fatalf("thumbnails = %d, %q: the page must get a smaller picture than the original", len(got), got[0].Name)
	}
	if none := get("a_unknown"); len(none) != 0 {
		t.Errorf("an unknown Ask has %d thumbnails", len(none))
	}
}
