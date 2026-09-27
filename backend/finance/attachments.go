package finance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// MaxAttachmentBytes is the largest receipt accepted (10 MB).
const MaxAttachmentBytes = 10 << 20

// AllowedMimes are the receipt formats, with the extension used when served.
var AllowedMimes = map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "application/pdf": "pdf"}

type Attachment struct {
	ID        int64
	SHA256    string
	Mime      string
	Size      int
	Data      []byte
	CreatedAt time.Time
}

// SniffMime detects the real type of a file from its bytes; the name or the
// declared type are never trusted.
func SniffMime(data []byte) string {
	mime := http.DetectContentType(data)
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = mime[:i]
	}
	return mime
}

// StoreAttachment validates and stores a receipt privately. The same file is
// stored once per workspace; reused reports whether it already existed.
func (s *Service) StoreAttachment(ctx context.Context, wsID int64, data []byte) (*Attachment, bool, error) {
	if len(data) == 0 {
		return nil, false, invalid("Arquivo vazio.")
	}
	if len(data) > MaxAttachmentBytes {
		return nil, false, invalid("O arquivo passa de 10 MB.")
	}
	mime := SniffMime(data)
	if _, ok := AllowedMimes[mime]; !ok {
		return nil, false, invalid("Formato não aceito: envie JPG, PNG, WEBP ou PDF.")
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	a := &Attachment{SHA256: hash, Mime: mime, Size: len(data)}
	var inserted bool
	err := s.DB.QueryRow(ctx, `
		INSERT INTO finance_attachments (workspace_id, sha256, mime, size_bytes, data)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, sha256) DO UPDATE SET sha256 = EXCLUDED.sha256
		RETURNING id, created_at, (xmax = 0)`, wsID, hash, mime, len(data), data).Scan(&a.ID, &a.CreatedAt, &inserted)
	if err != nil {
		return nil, false, err
	}
	return a, !inserted, nil
}

// GetAttachment reads a receipt of the workspace (never across workspaces).
func (s *Service) GetAttachment(ctx context.Context, wsID, id int64) (*Attachment, error) {
	a := &Attachment{ID: id}
	err := s.DB.QueryRow(ctx, `SELECT sha256, mime, size_bytes, data, created_at FROM finance_attachments
		WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&a.SHA256, &a.Mime, &a.Size, &a.Data, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}
