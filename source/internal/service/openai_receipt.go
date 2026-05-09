package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"

	"poker-bot/internal/domain"

	"github.com/disintegration/imaging"
	"github.com/shopspring/decimal"
)

type ReceiptOCR interface {
	ParseReceipt(ctx context.Context, image []byte, mimeType string, onRetry func()) (domain.ParsedReceipt, error)
}

type OpenAIReceiptOCR struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewOpenAIReceiptOCR(apiKey string, model string) *OpenAIReceiptOCR {
	return &OpenAIReceiptOCR{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (o *OpenAIReceiptOCR) ParseReceipt(ctx context.Context, image []byte, mimeType string, onRetry func()) (domain.ParsedReceipt, error) {
	if strings.TrimSpace(o.apiKey) == "" {
		return domain.ParsedReceipt{}, fmt.Errorf("OpenAI API key is not configured")
	}

	firstPrompt := strings.Join([]string{
		"Extract a printed restaurant receipt into JSON.",
		"Treat the image as a possibly skewed, rotated, perspective-distorted phone photo of a receipt. Mentally align the receipt before reading it.",
		"Use only printed text from the receipt. Ignore any handwritten text, shadows, table surface, fingers, background objects, blur artifacts, and decorative marks.",
		"Return only actual billable line items, the printed service amount if present, and the final printed total.",
		"Ignore headers and column names, dashed separators, subtotal/total/service labels as items, cashier or waiter names, table/check metadata, payment lines, footer text, and any replacement/modifier line that does not clearly have its own billable price.",
		"Do not invent item names. If a line has quantity and price but the item name is unreadable or mostly punctuation, exclude that line instead of guessing.",
		"Include a line item only if it clearly represents a real purchasable item with a readable name.",
		"Preserve item names exactly as printed when readable.",
	}, " ")
	retryPrompt := strings.Join([]string{
		"Extract a printed restaurant receipt into JSON and re-read it carefully for arithmetic consistency.",
		"Treat the image as a possibly skewed, rotated, perspective-distorted phone photo of a receipt. Mentally align the receipt before reading it.",
		"Use only printed text from the receipt. Ignore any handwritten text, shadows, table surface, fingers, background objects, blur artifacts, and decorative marks.",
		"Return only actual billable line items, the printed service amount if present, and the final printed total.",
		"Ignore headers and column names, dashed separators, subtotal/total/service labels as items, cashier or waiter names, table/check metadata, payment lines, footer text, and any replacement/modifier line that does not clearly have its own billable price.",
		"Do not invent item names. If a line has quantity and price but the item name is unreadable or mostly punctuation, exclude that line instead of guessing.",
		"Every returned line item must satisfy quantity * unit_price = line_total.",
		"The full receipt must satisfy sum(line_total for all returned items) + service_amount = total_amount exactly.",
		"If a line is ambiguous, exclude it rather than returning inconsistent arithmetic.",
		"Preserve item names exactly as printed when readable.",
	}, " ")

	variants, err := buildReceiptVariants(image)
	if err != nil {
		return domain.ParsedReceipt{}, fmt.Errorf("prepare receipt image variants: %w", err)
	}

	var lastErr error
	for i, variant := range variants {
		attemptLabel := fmt.Sprintf("attempt=%d variant=%s", i+1, variant.Label)
		prompt := retryPrompt
		if i == 0 {
			prompt = firstPrompt
		} else if i == 1 && onRetry != nil {
			onRetry()
		}

		result, err := o.parseReceiptAttempt(ctx, variant.Bytes, "image/jpeg", prompt, attemptLabel)
		if err != nil {
			log.Printf("receipt ocr %s request failed: %v", attemptLabel, err)
			lastErr = err
			continue
		}
		log.Printf("receipt ocr %s parsed: %s", attemptLabel, formatParsedReceiptForLog(result))

		if err := validateParsedReceipt(result); err != nil {
			subtotal, expectedTotal := receiptTotalsForLog(result)
			log.Printf("receipt ocr %s validation failed: %v | subtotal=%s service=%s expected_total=%s actual_total=%s", attemptLabel, err, subtotal.String(), result.ServiceAmount.String(), expectedTotal.String(), result.TotalAmount.String())
			lastErr = err
			continue
		}

		subtotal, expectedTotal := receiptTotalsForLog(result)
		log.Printf("receipt ocr %s validation passed: subtotal=%s service=%s expected_total=%s actual_total=%s", attemptLabel, subtotal.String(), result.ServiceAmount.String(), expectedTotal.String(), result.TotalAmount.String())
		result.Attempts = i + 1
		return result, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("receipt recognition failed")
	}
	log.Printf("receipt ocr final failure: %v", lastErr)

	return domain.ParsedReceipt{}, fmt.Errorf("не удалось уверенно распознать чек. Сфотографируйте чек еще раз более качественно")
}

func looksLikeReceiptItemName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	hasLetterOrDigit := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			hasLetterOrDigit = true
			break
		}
	}

	return hasLetterOrDigit
}

func (o *OpenAIReceiptOCR) parseReceiptAttempt(ctx context.Context, image []byte, mimeType string, prompt string, attemptLabel string) (domain.ParsedReceipt, error) {
	dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(image)
	requestBody := openAIResponsesRequest{
		Model: o.model,
		Input: []openAIInputMessage{
			{
				Role: "user",
				Content: []openAIInputContent{
					{
						Type: "input_text",
						Text: prompt,
					},
					{
						Type:     "input_image",
						ImageURL: dataURL,
						Detail:   "high",
					},
				},
			},
		},
		Text: openAITextConfig{
			Format: openAIFormat{
				Type:        "json_schema",
				Name:        "receipt_extraction",
				Description: "Printed receipt items and totals",
				Strict:      true,
				Schema: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"merchant_name":  map[string]any{"type": "string"},
						"service_amount": map[string]any{"type": "number"},
						"total_amount":   map[string]any{"type": "number"},
						"items": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]any{
									"name":       map[string]any{"type": "string"},
									"quantity":   map[string]any{"type": "integer", "minimum": 1},
									"unit_price": map[string]any{"type": "number", "minimum": 0},
									"line_total": map[string]any{"type": "number", "minimum": 0},
								},
								"required": []string{"name", "quantity", "unit_price", "line_total"},
							},
						},
					},
					"required": []string{"merchant_name", "service_amount", "total_amount", "items"},
				},
			},
		},
	}

	payload, err := json.Marshal(requestBody)
	if err != nil {
		return domain.ParsedReceipt{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewReader(payload))
	if err != nil {
		return domain.ParsedReceipt{}, err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return domain.ParsedReceipt{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return domain.ParsedReceipt{}, err
	}
	if resp.StatusCode >= 300 {
		return domain.ParsedReceipt{}, fmt.Errorf("OpenAI OCR failed: %s", strings.TrimSpace(string(body)))
	}

	var response openAIResponsesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return domain.ParsedReceipt{}, err
	}

	text := strings.TrimSpace(response.OutputText())
	if text == "" {
		return domain.ParsedReceipt{}, fmt.Errorf("OpenAI OCR returned empty response")
	}
	log.Printf("receipt ocr %s raw json: %s", attemptLabel, text)

	var parsed openAIParsedReceipt
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return domain.ParsedReceipt{}, fmt.Errorf("parse OCR JSON: %w", err)
	}

	items := make([]domain.ParsedReceiptItem, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		name := strings.TrimSpace(item.Name)
		if !looksLikeReceiptItemName(name) {
			continue
		}

		items = append(items, domain.ParsedReceiptItem{
			Name:      name,
			Quantity:  item.Quantity,
			UnitPrice: decimal.NewFromFloat(item.UnitPrice),
			LineTotal: decimal.NewFromFloat(item.LineTotal),
		})
	}

	return domain.ParsedReceipt{
		MerchantName:  strings.TrimSpace(parsed.MerchantName),
		Items:         items,
		ServiceAmount: decimal.NewFromFloat(parsed.ServiceAmount),
		TotalAmount:   decimal.NewFromFloat(parsed.TotalAmount),
	}, nil
}

func validateParsedReceipt(receipt domain.ParsedReceipt) error {
	if len(receipt.Items) == 0 {
		return fmt.Errorf("не удалось распознать ни одной корректной позиции")
	}
	if receipt.TotalAmount.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("не удалось распознать итоговую сумму")
	}

	itemsSubtotal := decimal.Zero
	for _, item := range receipt.Items {
		expectedLineTotal := item.UnitPrice.Mul(decimal.NewFromInt(int64(item.Quantity)))
		if !expectedLineTotal.Equal(item.LineTotal) {
			return fmt.Errorf("позиция %q не сходится: %s * %d != %s", item.Name, item.UnitPrice.String(), item.Quantity, item.LineTotal.String())
		}
		itemsSubtotal = itemsSubtotal.Add(item.LineTotal)
	}

	expectedTotal := itemsSubtotal.Add(receipt.ServiceAmount)
	if !expectedTotal.Equal(receipt.TotalAmount) {
		return fmt.Errorf("итог не сходится: позиции %s + сервис %s != итог %s", itemsSubtotal.String(), receipt.ServiceAmount.String(), receipt.TotalAmount.String())
	}

	return nil
}

func formatParsedReceiptForLog(receipt domain.ParsedReceipt) string {
	itemParts := make([]string, 0, len(receipt.Items))
	for i, item := range receipt.Items {
		itemParts = append(itemParts, fmt.Sprintf("#%d %q qty=%d unit=%s total=%s", i+1, item.Name, item.Quantity, item.UnitPrice.String(), item.LineTotal.String()))
	}

	return fmt.Sprintf(
		"merchant=%q service=%s total=%s items_count=%d items=[%s]",
		receipt.MerchantName,
		receipt.ServiceAmount.String(),
		receipt.TotalAmount.String(),
		len(receipt.Items),
		strings.Join(itemParts, "; "),
	)
}

func receiptTotalsForLog(receipt domain.ParsedReceipt) (decimal.Decimal, decimal.Decimal) {
	subtotal := decimal.Zero
	for _, item := range receipt.Items {
		subtotal = subtotal.Add(item.LineTotal)
	}
	return subtotal, subtotal.Add(receipt.ServiceAmount)
}

type receiptImageVariant struct {
	Label string
	Bytes []byte
}

func buildReceiptVariants(source []byte) ([]receiptImageVariant, error) {
	srcImage, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("decode receipt image: %w", err)
	}

	base := imaging.Clone(srcImage)
	enhanced := applyReceiptEnhancement(base)

	variantImages := []struct {
		Label string
		Image image.Image
	}{
		{Label: "original", Image: base},
		{Label: "enhanced", Image: enhanced},
		{Label: "enhanced_rotated_minus_10", Image: imaging.Rotate(enhanced, -10, color.NRGBA{R: 255, G: 255, B: 255, A: 255})},
		{Label: "enhanced_rotated_plus_10", Image: imaging.Rotate(enhanced, 10, color.NRGBA{R: 255, G: 255, B: 255, A: 255})},
	}

	variants := make([]receiptImageVariant, 0, len(variantImages))
	for _, variant := range variantImages {
		encoded, err := encodeReceiptVariant(variant.Image)
		if err != nil {
			return nil, err
		}
		variants = append(variants, receiptImageVariant{
			Label: variant.Label,
			Bytes: encoded,
		})
	}

	return variants, nil
}

func applyReceiptEnhancement(src image.Image) image.Image {
	img := imaging.Grayscale(src)
	img = imaging.AdjustContrast(img, 20)
	img = imaging.Sharpen(img, 0.8)
	return img
}

func encodeReceiptVariant(img image.Image) ([]byte, error) {
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 92}); err != nil {
		return nil, fmt.Errorf("encode receipt variant: %w", err)
	}
	return buffer.Bytes(), nil
}

type openAIResponsesRequest struct {
	Model string               `json:"model"`
	Input []openAIInputMessage `json:"input"`
	Text  openAITextConfig     `json:"text"`
}

type openAIInputMessage struct {
	Role    string               `json:"role"`
	Content []openAIInputContent `json:"content"`
}

type openAIInputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type openAITextConfig struct {
	Format openAIFormat `json:"format"`
}

type openAIFormat struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Strict      bool           `json:"strict"`
	Schema      map[string]any `json:"schema"`
}

type openAIResponsesResponse struct {
	Output []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func (r openAIResponsesResponse) OutputText() string {
	for _, output := range r.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" {
				return content.Text
			}
		}
	}
	return ""
}

type openAIParsedReceipt struct {
	MerchantName  string  `json:"merchant_name"`
	ServiceAmount float64 `json:"service_amount"`
	TotalAmount   float64 `json:"total_amount"`
	Items         []struct {
		Name      string  `json:"name"`
		Quantity  int     `json:"quantity"`
		UnitPrice float64 `json:"unit_price"`
		LineTotal float64 `json:"line_total"`
	} `json:"items"`
}
