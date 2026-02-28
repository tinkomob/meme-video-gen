package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/genai"

	"meme-video-gen/internal/logging"
	"meme-video-gen/internal/model"
)

type TitleGenerator struct {
	apiKey string
	log    *logging.Logger
}

func NewTitleGenerator(apiKey string, log *logging.Logger) *TitleGenerator {
	return &TitleGenerator{apiKey: apiKey, log: log}
}

func (tg *TitleGenerator) GenerateTitleForMeme(ctx context.Context, song *model.Song) (string, error) {
	if tg.apiKey == "" {
		tg.log.Infof("ai: no api key, using fallback title")
		return fmt.Sprintf("Мем под трек: %s", song.Title), nil
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  tg.apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return "", fmt.Errorf("genai client: %w", err)
	}

	prompt := fmt.Sprintf(
		"Ты — креативный копирайтер для коротких видео. "+
			"Создай одно короткое (до 60 символов), цепляющее название для 8-секундного мем-видео под трек '%s'. "+
			"Название должно быть на русском, без эмодзи, без хэштегов, просто текст.",
		song.Title,
	)

	resp, err := client.Models.GenerateContent(ctx, "gemini-2.0-flash", []*genai.Content{
		genai.NewContentFromText(prompt, genai.RoleUser),
	}, nil)
	if err != nil {
		return "", fmt.Errorf("generate content: %w", err)
	}

	title := resp.Text()
	if title == "" {
		title = fmt.Sprintf("Мем под трек: %s", song.Title)
	}
	return title, nil
}

// GenerateIdeaForSong generates a creative video idea based on the track, divided into scenes
// Each scene is designed for a 6-second video clip
func (tg *TitleGenerator) GenerateIdeaForSong(ctx context.Context, song *model.Song) ([]string, error) {
	if tg.apiKey == "" {
		tg.log.Infof("ai: no api key, using fallback ideas")
		return []string{
			"[ВАЙБ]\nАтмосферный трек '" + song.Title + "' с гипнотичным, медитативным настроением.",
			"[ИДЕЯ]\nМакро-съёмка музыкальных инструментов и аудиотехники в тихом студийном свете — визуальная медитация под звук.",
			"[ПРОМПТ]\nExtreme close-up of vinyl record spinning in slow motion, soft bokeh background, dramatic side lighting with warm amber glow, cinematic minimalist aesthetic, 4K, ultra slow motion, soft focus edges, atmospheric dust particles floating in light beam.",
		}, nil
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  tg.apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("genai client: %w", err)
	}

	prompt := fmt.Sprintf(
		"Роль: Ты — профессиональный Арт-директор и эксперт по визуализации звука. "+
			"Твоя специализация — создание гипнотичного видеоряда для Reels и TikTok, который не отвлекает от музыки, а заставляет зрителя вслушиваться в неё.\n\n"+
			"Трек: '%s', Артист: '%s'\n\n"+
			"Твоя задача:\n"+
			"1. Проанализируй трек: определи вероятный темп (BPM), настроение, ключевые инструменты и текстуры (например, виниловый шум, эхо, мягкое пианино).\n"+
			"2. Предложи концепцию видео, напрямую связанную с музыкальной культурой (инструменты, аудиотехника, эстетика студии или абстрактные визуализации звука).\n"+
			"3. Напиши детальный промпт для генерации видео в ИИ (Runway, Luma, Kling) на английском языке.\n\n"+
			"Требования к промпту:\n"+
			"- Стиль: Cinematic, Minimalist, Aesthetic\n"+
			"- Кадр: Macro-shot или Close-up (крупные планы)\n"+
			"- Движение: Очень медленное (Slow motion), плавное, гипнотическое\n"+
			"- Освещение: Dramatic lighting, soft glows, атмосферные тени\n"+
			"- Фокус: Bokeh и Soft focus, чтобы картинка была «мягкой» и не перегруженной деталями\n\n"+
			"Формат ответа (строго соблюдай структуру):\n"+
			"[ВАЙБ]\n"+
			"[краткое описание вайба трека]\n\n"+
			"[ИДЕЯ]\n"+
			"[описание идеи видео и почему это подходит треку]\n\n"+
			"[ПРОМПТ]\n"+
			"[готовый промпт на английском языке для ИИ-генерации видео]",
		song.Title,
		song.Author,
	)

	resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash", []*genai.Content{
		genai.NewContentFromText(prompt, genai.RoleUser),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("generate content: %w", err)
	}

	content := resp.Text()
	if content == "" {
		return []string{
			"[ВАЙБ]\nАтмосферный трек с гипнотичным, медитативным настроением.",
			"[ИДЕЯ]\nМакро-съёмка музыкальных инструментов и аудиотехники — визуальная медитация под звук.",
			"[ПРОМПТ]\nExtreme close-up of vinyl record spinning in slow motion, soft bokeh background, dramatic side lighting with warm amber glow, cinematic minimalist aesthetic, 4K, ultra slow motion.",
		}, nil
	}

	// Parse sections [ВАЙБ], [ИДЕЯ], [ПРОМПТ] from response
	sections := []string{"[ВАЙБ]", "[ИДЕЯ]", "[ПРОМПТ]"}
	var result []string
	for i, section := range sections {
		start := strings.Index(content, section)
		if start == -1 {
			continue
		}
		start += len(section)
		end := len(content)
		if i+1 < len(sections) {
			if next := strings.Index(content[start:], sections[i+1]); next != -1 {
				end = start + next
			}
		}
		body := strings.TrimSpace(content[start:end])
		if body != "" {
			result = append(result, section+"\n"+body)
		}
	}

	if len(result) > 0 {
		return result, nil
	}

	// Fallback if parsing failed
	return []string{content}, nil
}
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func GetAPIKey() string {
	key := os.Getenv("GOOGLE_API_KEY")
	if key == "" {
		key = os.Getenv("GEMINI_API_KEY")
	}
	return key
}

// GetRandomFact retrieves a random fact from a public API
func GetRandomFact(ctx context.Context) string {
	// Try to get a fact from uselessfacts API
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://uselessfacts.jsph.pl/random.json?language=en", nil)
	if err != nil {
		return "Did you know? Meme videos are the best! 🎬"
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")

	resp, err := client.Do(req)
	if err != nil {
		return "Did you know? Meme videos are the best! 🎬"
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "Did you know? Meme videos are the best! 🎬"
	}

	var result struct {
		Text string `json:"text"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "Did you know? Meme videos are the best! 🎬"
	}

	if result.Text != "" {
		return result.Text
	}

	return "Did you know? Meme videos are the best! 🎬"
}
