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
			"💡 Основная идея: Визуальный рассказ через метафоры и символы, синхронизированный с ритмом музыки\n\nСцена 1: Динамичные переходы и ключевые визуальные элементы под музыку '" + song.Title + "'",
			"Сцена 2: Крупные планы, зум и цветовые фильтры для усиления эмоции",
			"Сцена 3: Быстрые смены кадров и финальный момент импакта в ритм музыки",
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
			"На основе трека '%s' (артист %s) создай ОДНУ цельную и оригинальную идею для короткого видеоролика. "+
			"Опиши основную концепцию, а затем разбей её на 3-5 СВЯЗАННЫХ сцен. "+
			"Каждая сцена продлится 6 секунд и должна логически вытекать из предыдущей, создавая единый видеоуж.\n\n"+
			"Формат ответа:\n"+
			"💡 Основная идея: [описание общей концепции и визуального стиля]\n\n"+
			"Сцена 1: [описание первой сцены]\n"+
			"Сцена 2: [описание второй сцены]\n"+
			"[и так далее...]\n\n"+
			"Для каждой сцены опиши:\n"+
			"- Какие визуальные элементы/объекты использовать\n"+
			"- Какой стиль и эффекты\n"+
			"- Какой темп и динамика движения\n"+
			"- Как эта сцена переходит в следующую\n\n"+
			"Требования:\n"+
			"- Сцены должны быть визуально красивыми, эстетичными и связанными одной идеей\n"+
			"- Легко снимаемыми с помощью мобильного телефона или простых материалов\n"+
			"- БЕЗ текста внутри видео",
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
			"💡 Основная идея: Визуальный рассказ через метафоры и символы, синхронизированный с ритмом музыки\n\nСцена 1: Динамичные переходы и ключевые визуальные элементы под музыку '" + song.Title + "'",
			"Сцена 2: Крупные планы, зум и цветовые фильтры для усиления эмоции",
			"Сцена 3: Быстрые смены кадров и финальный момент импакта в ритм музыки",
		}, nil
	}

	// Parse the response into individual scenes
	var scenes []string
	lines := strings.Split(strings.TrimSpace(content), "\n")
	var currentScene string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Check if this is a scene header (starts with "Сцена")
		if strings.HasPrefix(line, "Сцена") && strings.Contains(line, ":") {
			if currentScene != "" {
				scenes = append(scenes, currentScene)
			}
			currentScene = line
		} else if currentScene != "" {
			// Append continuation to current scene
			currentScene += "\n" + line
		}
	}
	// Add last scene
	if currentScene != "" {
		scenes = append(scenes, currentScene)
	}

	// If parsing failed or very few scenes, return fallback
	if len(scenes) < 2 {
		return []string{
			"💡 Основная идея: " + content + "\n\nСцена 1: Начало с привлечения внимания и установки настроения",
			"Сцена 2: Развитие основной идеи и усиление визуального эффекта",
			"Сцена 3: Финальный момент и впечатление",
		}, nil
	}

	return scenes, nil
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
