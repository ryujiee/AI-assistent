package finance

import "context"

// ReceiptReader reads receipts (images and PDFs) into a Receipt.
type ReceiptReader struct{}

func (r *ReceiptReader) Read(ctx context.Context, item *InboxItem, t *turn) error {
	return &PermanentError{Code: "receipts_unavailable", Reply: "⚠️ Ainda não consigo ler comprovantes. Pode escrever o valor e o que foi?"}
}
