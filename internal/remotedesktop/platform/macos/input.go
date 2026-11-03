package macos

import (
	"context"

	"github.com/thewei/solco/internal/remotedesktop/helperipc"
	"github.com/thewei/solco/internal/remotedesktop/platform"
)

// input sends fire-and-forget commands to the helper, which holds the pressed-state needed for
// ReleaseAll. Failures (Accessibility revoked mid-session) come back as platform events.
type input struct{ conn *helperipc.Conn }

var _ platform.Input = (*input)(nil)

func (i *input) PointerMove(x, y float64) error {
	return i.conn.Send(helperipc.OpInputMouse, helperipc.MouseArgs{Action: "move", X: x, Y: y})
}

func (i *input) PointerButton(button string, down bool, x, y float64, clickCount int) error {
	action := "up"
	if down {
		action = "down"
	}
	return i.conn.Send(helperipc.OpInputMouse, helperipc.MouseArgs{Action: action, Button: button, X: x, Y: y, ClickCount: clickCount})
}

func (i *input) Scroll(dx, dy float64) error {
	return i.conn.Send(helperipc.OpInputScroll, helperipc.ScrollArgs{DX: dx, DY: dy})
}

func (i *input) Key(code string, down bool) error {
	vk, err := KeyCode(code)
	if err != nil {
		return err
	}
	return i.conn.Send(helperipc.OpInputKey, helperipc.KeyArgs{Code: vk, Down: down})
}

func (i *input) Text(text string) error {
	return i.conn.Send(helperipc.OpInputText, helperipc.TextArgs{Text: text})
}

func (i *input) ReleaseAll() error { return i.conn.Send(helperipc.OpInputReleaseAll, nil) }

type clipboard struct{ conn *helperipc.Conn }

var _ platform.Clipboard = (*clipboard)(nil)

func (c *clipboard) ReadText(ctx context.Context) (string, error) {
	var out struct {
		Text string `json:"text"`
	}
	err := c.conn.Call(ctx, helperipc.OpClipboardRead, nil, &out)
	return out.Text, err
}

func (c *clipboard) WriteText(ctx context.Context, text string) error {
	return c.conn.Call(ctx, helperipc.OpClipboardWrite, helperipc.TextArgs{Text: text}, nil)
}
