package whatsapp

import (
	"encoding/base64"
	"log/slog"

	"github.com/skip2/go-qrcode"
)

// fakeMode is set when WHATSAPP_FAKE is configured: the process never opens a
// WhatsApp connection, so local development and screenshots cannot touch a
// real account.
var fakeMode bool

// InitFake replaces the WhatsApp connection with local state. mode "qr" shows
// a dummy pairing code; anything else simulates a linked device.
func InitFake(mode string) {
	fakeMode = true
	QRMutex.Lock()
	defer QRMutex.Unlock()
	if mode == "qr" {
		png, err := qrcode.Encode("fake-pairing-code-for-local-development", qrcode.Medium, 256)
		if err == nil {
			LatestQRCode = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
		IsConnected = false
	} else {
		IsConnected = true
	}
	slog.Warn("whatsapp.fake_mode", "mode", mode, "note", "no WhatsApp connection is opened")
}
