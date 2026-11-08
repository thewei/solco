package remotehost

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type savedAttachment struct {
	data       []byte
	mime, name string
}

func attachHarness(t *testing.T) (*harness, *[]savedAttachment) {
	t.Helper()
	h := newHarness(t)
	var saved []savedAttachment
	h.srv.opts.Attach = func(data []byte, mime, name string) (string, error) {
		saved = append(saved, savedAttachment{data, mime, name})
		return "/cache/Solco/ChatAttachments/20261031-000000-abc.png", nil
	}
	return h, &saved
}

func attachBody(data []byte, mime, name string) string {
	raw, _ := json.Marshal(attachRequest{DataBase64: base64.StdEncoding.EncodeToString(data), Mime: mime, Name: name})
	return string(raw)
}

func TestAttachStoresTheUploadAndAnswersItsPath(t *testing.T) {
	h, saved := attachHarness(t)
	req, _ := http.NewRequest("POST", h.http.URL+"/api/v3/panes/w1:p1/chat/attachments?instance=default", strings.NewReader(attachBody([]byte("png-bytes"), "image/png", "shot.png")))
	req.Header.Set("Authorization", "Bearer "+h.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out attachReply
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil || !strings.HasSuffix(out.Path, ".png") {
		t.Fatalf("status %d path %q", resp.StatusCode, out.Path)
	}
	if len(*saved) != 1 || string((*saved)[0].data) != "png-bytes" || (*saved)[0].mime != "image/png" || (*saved)[0].name != "shot.png" {
		t.Fatalf("saved %+v", *saved)
	}
}

func TestAttachRefusals(t *testing.T) {
	h, saved := attachHarness(t)
	good := attachBody([]byte("x"), "image/png", "a.png")
	for _, c := range []struct {
		name, path, token, body string
		status                  int
		code                    string
	}{
		{"no token", "/api/v3/panes/w1:p1/chat/attachments?instance=default", "", good, 401, "unauthorized"},
		{"unknown pane", "/api/v3/panes/nope/chat/attachments?instance=default", h.token, good, 404, "unknown_pane"},
		{"unknown instance", "/api/v3/panes/w1:p1/chat/attachments?instance=ghost", h.token, good, 404, "unknown_instance"},
		{"not json", "/api/v3/panes/w1:p1/chat/attachments?instance=default", h.token, "nope", 400, "bad_request"},
		{"not base64", "/api/v3/panes/w1:p1/chat/attachments?instance=default", h.token, `{"dataBase64":"@@@"}`, 400, "bad_request"},
		{"empty", "/api/v3/panes/w1:p1/chat/attachments?instance=default", h.token, `{"dataBase64":""}`, 400, "bad_request"},
	} {
		status, got := h.post(c.path, c.token, c.body)
		if status != c.status || !strings.HasPrefix(got, c.code+"|") {
			t.Fatalf("%s: status %d (%s), want %d %s", c.name, status, got, c.status, c.code)
		}
	}
	if len(*saved) != 0 {
		t.Fatal("a refused upload reached storage")
	}
}

func TestAttachUnavailableWithoutAStore(t *testing.T) {
	h := newHarness(t)
	status, got := h.post("/api/v3/panes/w1:p1/chat/attachments?instance=default", h.token, attachBody([]byte("x"), "", ""))
	if status != 404 || !strings.HasPrefix(got, "attach_unavailable|") {
		t.Fatalf("status %d (%s)", status, got)
	}
	var caps struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&caps) != nil || caps.Capabilities["attachments"] {
		t.Fatalf("a Host without a store must not advertise attachments: %+v", caps)
	}
}

func TestBootstrapAdvertisesAttachmentsWhenThereIsAStore(t *testing.T) {
	h, _ := attachHarness(t)
	var caps struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&caps) != nil || !caps.Capabilities["attachments"] {
		t.Fatalf("capabilities %+v", caps)
	}
}

func TestAttachTooLargeIsRefusedBeforeStorage(t *testing.T) {
	h, saved := attachHarness(t)
	big := make([]byte, 32<<20+1)
	status, got := h.post("/api/v3/panes/w1:p1/chat/attachments?instance=default", h.token, attachBody(big, "application/pdf", "big.pdf"))
	if status != http.StatusRequestEntityTooLarge || !strings.HasPrefix(got, "attachment_too_large|") {
		t.Fatalf("status %d (%s)", status, got)
	}
	if len(*saved) != 0 {
		t.Fatal("an oversized upload reached storage")
	}
}
