package protocol

import "testing"

func TestTierSize(t *testing.T) {
	retina := DisplayInfo{PixelWidth: 3600, PixelHeight: 2338}
	hd := DisplayInfo{PixelWidth: 1920, PixelHeight: 1080}
	small := DisplayInfo{PixelWidth: 1024, PixelHeight: 600}
	ultra := DisplayInfo{PixelWidth: 10240, PixelHeight: 2880}
	cases := []struct {
		name string
		tier Tier
		d    DisplayInfo
		w, h int
	}{
		{"1080p of a retina panel keeps its 1.54 aspect", TierStandard, retina, 1662, 1080},
		{"720p of 16:9", TierSmooth, hd, 1280, 720},
		{"1440p never upscales a 1080p panel", TierHigh, hd, 1920, 1080},
		{"a small panel is not upscaled", TierStandard, small, 1024, 600},
		{"ultra-wide is width-capped, aspect kept", TierHigh, ultra, 4096, 1152},
	}
	for _, c := range cases {
		w, h := Specs[c.tier].Size(c.d)
		if w != c.w || h != c.h {
			t.Errorf("%s: got %dx%d want %dx%d", c.name, w, h, c.w, c.h)
		}
		if w%2 != 0 || h%2 != 0 {
			t.Errorf("%s: odd size %dx%d", c.name, w, h)
		}
	}
	if w, h := Specs[TierStandard].Size(DisplayInfo{}); w != 0 || h != 0 {
		t.Error("empty display must give a zero size")
	}
}

func TestDecode(t *testing.T) {
	good := map[string]string{
		TypePointerMove:   `{"type":"pointer.move","seq":3,"x":0.5,"y":0.25}`,
		TypePointerButton: `{"type":"pointer.button","seq":4,"x":0.1,"y":0.2,"button":"left","down":true}`,
		TypeKey:           `{"type":"key","code":"KeyA","down":true}`,
		TypeTextInsert:    `{"type":"text.insert","text":"你好"}`,
		TypeKeyframe:      `{"type":"keyframe.request"}`,
	}
	for typ, body := range good {
		if _, err := Decode([]byte(body)); err != nil {
			t.Errorf("%s: %v", typ, err)
		}
	}
	for _, body := range []string{``, `{`, `{"type":"nope"}`, `{"type":"pointer.move","x":"a"}`} {
		if _, err := Decode([]byte(body)); err == nil {
			t.Errorf("%q must fail", body)
		}
	}
	if _, err := Decode(make([]byte, MaxMessageBytes+1)); err == nil {
		t.Error("oversized message must fail")
	}
}

func TestAdaptBitrate(t *testing.T) {
	if got := AdaptBitrate(TierStandard, 100000); got != 5000 {
		t.Fatalf("a roomy link keeps the tier target, got %d", got)
	}
	if got := AdaptBitrate(TierStandard, 2000); got != 1700 {
		t.Fatalf("a tight link gets 85%%, got %d", got)
	}
	if got := AdaptBitrate(TierStandard, 100); got != 1000 {
		t.Fatalf("the floor is a fifth of the tier, got %d", got)
	}
	if got := AdaptBitrate(TierSmooth, 100); got != 500 {
		t.Fatalf("smooth floor, got %d", got)
	}
}

func TestWorthRetuning(t *testing.T) {
	if !WorthRetuning(0, 100) || !WorthRetuning(1000, 1100) || !WorthRetuning(1000, 880) {
		t.Fatal("10% moves are real")
	}
	if WorthRetuning(1000, 1050) || WorthRetuning(1000, 950) {
		t.Fatal("small moves are noise")
	}
}
