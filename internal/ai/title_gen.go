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
			"[СЦЕНА 1]\nДинамичные переходы и ключевые визуальные элементы под музыку '" + song.Title + "'. Резкое начало с импакт-элемента.",
			"[СЦЕНА 2]\nКрупные планы, зум и цветовые фильтры для усиления эмоции. Резкий переход.",
			"[СЦЕНА 3]\nБыстрые смены кадров и финальный момент импакта в ритм музыки. Резкое завершение.",
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
		"Ты — креативный режиссер для TikTok и Reels. "+
			"На основе трека '%s' (артист %s) создай оригинальную идею из 3-5 сцен. "+
			"Каждая сцена продлится 6 секунд и должна резко переходить в следующую.\n\n"+
			"Формат ответа (БЕЗ вводной концепции, только сцены):\n"+
			"[СЦЕНА 1]\n"+
			"[описание первой сцены]\n\n"+
			"[СЦЕНА 2]\n"+
			"[описание второй сцены]\n\n"+
			"[СЦЕНА 3]\n"+
			"[описание третьей сцены]\n\n"+
			"[и так далее...]\n\n"+
			"Для каждой сцены напиши:\n"+
			"- Какие визуальные элементы/объекты использовать\n"+
			"- Какой стиль и эффекты (фильтры, переходы)\n"+
			"- Динамика и темп движения\n"+
			"Требования:\n"+
			"- КРИТИЧНО: между сценами ОБЯЗАТЕЛЬНО резкие переходы\n"+
			"- Сцены должны быть визуально красивыми и эстетичными\n"+
			"- Легко снимаемыми с мобильного телефона\n"+
			"- БЕЗ какого-либо текста внутри видео\n"+
			"- НЕ описывай основную идею/концепцию, сразу пиши сцены",
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
			"[СЦЕНА 1]\nДинамичные переходы и ключевые визуальные элементы под музыку. Резкое начало с импакт-элемента.",
			"[СЦЕНА 2]\nКрупные планы, зум и цветовые фильтры для усиления эмоции. Резкий переход.",
			"[СЦЕНА 3]\nБыстрые смены кадров и финальный момент импакта. Резкое завершение.",
		}, nil
	}

	// Split content by double newlines to get individual scenes
	// Return as-is without parsing
	var scenes []string
	parts := strings.Split(content, "\n\n")
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			scenes = append(scenes, trimmed)
		}
	}

	// If we got at least one scene, return it
	if len(scenes) > 0 {
		return scenes, nil
	}

	// Fallback if something went wrong
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
