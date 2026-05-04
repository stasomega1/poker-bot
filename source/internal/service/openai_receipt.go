package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"poker-bot/internal/domain"

	"github.com/shopspring/decimal"
)

type ReceiptOCR interface {
	ParseReceipt(ctx context.Context, image []byte, mimeType string) (domain.ParsedReceipt, error)
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

func (o *OpenAIReceiptOCR) ParseReceipt(ctx context.Context, image []byte, mimeType string) (domain.ParsedReceipt, error) {
	if strings.TrimSpace(o.apiKey) == "" {
		return domain.ParsedReceipt{}, fmt.Errorf("OpenAI API key is not configured")
	}

	dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(image)
	requestBody := openAIResponsesRequest{
		Model: o.model,
		Input: []openAIInputMessage{
			{
				Role: "user",
				Content: []openAIInputContent{
					{
						Type: "input_text",
						Text: "Extract the printed restaurant receipt into JSON. Ignore all handwritten text and markings. Return only printed line items from the receipt, the printed service amount if present, and the final printed total. Preserve the product names as they appear on the receipt.",
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

	var parsed openAIParsedReceipt
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return domain.ParsedReceipt{}, fmt.Errorf("parse OCR JSON: %w", err)
	}

	items := make([]domain.ParsedReceiptItem, 0, len(parsed.Items))
	itemsSubtotal := decimal.Zero
	for _, item := range parsed.Items {
		unitPrice := decimal.NewFromFloat(item.UnitPrice)
		lineTotal := decimal.NewFromFloat(item.LineTotal)
		items = append(items, domain.ParsedReceiptItem{
			Name:      strings.TrimSpace(item.Name),
			Quantity:  item.Quantity,
			UnitPrice: unitPrice,
			LineTotal: lineTotal,
		})
		itemsSubtotal = itemsSubtotal.Add(lineTotal)
	}

	result := domain.ParsedReceipt{
		MerchantName:  strings.TrimSpace(parsed.MerchantName),
		Items:         items,
		ServiceAmount: decimal.NewFromFloat(parsed.ServiceAmount),
		TotalAmount:   decimal.NewFromFloat(parsed.TotalAmount),
	}

	if result.TotalAmount.LessThanOrEqual(decimal.Zero) {
		result.TotalAmount = itemsSubtotal.Add(result.ServiceAmount)
	}

	return result, nil
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
