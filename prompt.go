package claudeagentsdk

import (
	"encoding/base64"
	"fmt"
	"net/url"
)

// Prompt accepts text or a list of user input blocks.
type Prompt interface {
	string | []InputBlock
}

// InputBlock is a text or image block sent in a user prompt.
// Text blocks require Text and no Source. Image blocks require Source and no Text.
type InputBlock struct {
	Type   string       `json:"type"`
	Text   string       `json:"text,omitempty"`
	Source *ImageSource `json:"source,omitempty"`
}

// ImageSource contains either base64 Data with MediaType or an HTTP(S) URL.
// Type must be "base64" or "url", respectively.
type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

func validatePrompt[P Prompt](prompt P) error {
	blocks, ok := any(prompt).([]InputBlock)
	if !ok {
		return nil
	}
	if len(blocks) == 0 {
		return fmt.Errorf("prompt: input blocks must not be empty")
	}
	for i, block := range blocks {
		if err := block.validate(); err != nil {
			return fmt.Errorf("prompt block %d: %w", i, err)
		}
	}
	return nil
}

func (b InputBlock) validate() error {
	switch b.Type {
	case "text":
		if b.Text == "" || b.Source != nil {
			return fmt.Errorf("text block requires nonempty text and no source")
		}
	case "image":
		if b.Text != "" || b.Source == nil {
			return fmt.Errorf("image block requires a source and no text")
		}
		s := b.Source
		switch s.Type {
		case "base64":
			if s.Data == "" || s.URL != "" {
				return fmt.Errorf("base64 source requires data and no URL")
			}
			switch s.MediaType {
			case "image/jpeg", "image/png", "image/gif", "image/webp":
			default:
				return fmt.Errorf("unsupported image media type %q", s.MediaType)
			}
			if _, err := base64.StdEncoding.Strict().DecodeString(s.Data); err != nil {
				return fmt.Errorf("invalid base64 image data: %w", err)
			}
		case "url":
			u, err := url.Parse(s.URL)
			if err != nil {
				return fmt.Errorf("invalid image URL: %w", err)
			}
			if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || s.Data != "" || s.MediaType != "" {
				return fmt.Errorf("URL source requires an absolute HTTP(S) URL and no data or media type")
			}
		default:
			return fmt.Errorf("unsupported image source type %q", s.Type)
		}
	default:
		return fmt.Errorf("unsupported input block type %q", b.Type)
	}
	return nil
}
