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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
		return "", fmt.Errorf("ai: no api key configured")
	}

	// Retry strategy: 3 attempts with exponential backoff
	const maxRetries = 3
	const initialBackoff = 2 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		title, err := tg.generateTitleWithClient(ctx, song)

		// Success - return immediately
		if err == nil && title != "" {
			return title, nil
		}

		// Check if error is retryable
		isRetryable := tg.isRetryableError(err)

		// Log the error
		if attempt < maxRetries && isRetryable {
			backoff := initialBackoff * time.Duration(1<<uint(attempt-1))
			tg.log.Warnf("ai: generate title failed (attempt %d/%d): %v. Retrying in %v",
				attempt, maxRetries, err, backoff)

			// Wait with backoff before retry
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return "", fmt.Errorf("context cancelled: %w", ctx.Err())
			}
			continue
		}

		// Last attempt or non-retryable error
		if attempt == maxRetries && isRetryable {
			return "", fmt.Errorf("all %d retry attempts exhausted: %w", maxRetries, err)
		}

		// Non-retryable error or empty response on any attempt
		return "", fmt.Errorf("generate title failed: %w", err)
	}

	return "", fmt.Errorf("generate title failed: unknown error")
}

// generateTitleWithClient makes the actual API call for title generation
func (tg *TitleGenerator) generateTitleWithClient(ctx context.Context, song *model.Song) (string, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  tg.apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return "", fmt.Errorf("genai client: %w", err)
	}

	prompt := fmt.Sprintf(
		"Ты — креативный копирайтер для коротких видео. "+
			"Создай одно короткое (до 60 символов), цепляющее название для 12-секундного мем-видео под трек '%s'. "+
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
		return "", fmt.Errorf("empty response from gemini api")
	}
	return title, nil
}

// GenerateIdeaForSong generates a creative video idea based on the track, divided into scenes
// Each scene is designed for a 3-4 second segment within a 12-second video
// Uses exponential backoff retry for API failures (503, 429, 500, etc)
func (tg *TitleGenerator) GenerateIdeaForSong(ctx context.Context, song *model.Song) ([]string, error) {
	if tg.apiKey == "" {
		return nil, fmt.Errorf("ai: no api key configured")
	}

	// Retry strategy: 3 attempts with exponential backoff
	const maxRetries = 3
	const initialBackoff = 2 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		ideas, err := tg.generateIdeaWithClient(ctx, song)

		// Success - return immediately
		if err == nil {
			return ideas, nil
		}

		// Check if error is retryable (503, 429, 500, timeout, etc)
		isRetryable := tg.isRetryableError(err)

		// Log the error
		if attempt < maxRetries && isRetryable {
			backoff := initialBackoff * time.Duration(1<<uint(attempt-1))
			tg.log.Warnf("ai: generate idea failed (attempt %d/%d): %v. Retrying in %v",
				attempt, maxRetries, err, backoff)

			// Wait with backoff before retry
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
			}
			continue
		}

		// Last attempt or non-retryable error
		if attempt == maxRetries && isRetryable {
			return nil, fmt.Errorf("all %d retry attempts exhausted: %w", maxRetries, err)
		}

		// Non-retryable error on any attempt
		return nil, fmt.Errorf("generate idea failed: %w", err)
	}

	return nil, fmt.Errorf("generate idea failed: unknown error")
}

// generateIdeaWithClient makes the actual API call
func (tg *TitleGenerator) generateIdeaWithClient(ctx context.Context, song *model.Song) ([]string, error) {
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
			"2. Предложи одну концепцию видео, но каждый раз выбирай новую комбинацию деталей, чтобы концепции отличались друг от друга между разными запросами: меняй главный объект, ракурс, свет, текстуру, настроение и мелкие визуальные акценты, сохраняя общий стиль. Используй музыкальную культуру, аудиотехнику, студийную эстетику или абстрактные визуализации звука. Концепция должна быть рассчитана на 12 секунд и разбита на 3-4 сцены.\n"+
			"3. Напиши один детальный промпт для генерации видео в ИИ (Runway, Luma, Kling) на английском языке, основанный только на этой одной концепции и рассчитанный на 12 секунд с 3-4 сценами.\n\n"+
			"Требования к концепции и промпту:\n"+
			"- Общий стиль: Cinematic, Minimalist, Aesthetic\n"+
			"- Палитра: тёплая, мягкая, приглушённая, без резких цветовых контрастов\n"+
			"- Кадр: Macro-shot или Close-up, допускаются редкие средние планы\n"+
			"- Движение: Очень медленное и плавное, но с чёткими, выразительными переходами между сценами\n"+
			"- Освещение: Dramatic lighting, soft glows, атмосферные тени\n"+
			"- Фокус: Bokeh, Soft focus, лёгкая дымка, чтобы картинка была мягкой и не перегруженной деталями\n"+
			"- Структура видео: 3-4 сцены общей длительностью 12 секунд с резкими переходами, которые помогают создать эффект зацикливания\n"+
			"- Зацикливаемость: Концовка должна легко переходить в начало, сохраняя ритм и визуальную энергию\n"+
			"- Внутренняя вариативность: каждая новая генерация должна быть заметно другой по объекту, свету и фактуре, но оставаться в том же эстетическом семействе\n\n"+
			"Формат ответа (строго соблюдай структуру):\n"+
			"[ВАЙБ]\n"+
			"[краткое описание вайба трека]\n\n"+
			"[ИДЕЯ]\n"+
			"[одна короткая, законченная концепция видео]\n\n"+
			"[ПРОМПТ]\n"+
			"[готовый промпт на английском языке для ИИ-генерации видео, основанный на лучшей идее]",
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
		// API returned empty response - treat as error
		return nil, fmt.Errorf("empty response from gemini api")
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

// isRetryableError determines if an error should trigger a retry
func (tg *TitleGenerator) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Check for known retryable errors
	retryablePatterns := []string{
		"503",         // Service Unavailable
		"429",         // Too Many Requests
		"500",         // Internal Server Error
		"502",         // Bad Gateway
		"timeout",     // Context timeout
		"unavailable", // gRPC UNAVAILABLE
		"temporarily unavailable",
		"Please try again later",
		"high demand",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	// Check if it's an unavailable status code
	if st, ok := status.FromError(err); ok {
		return st.Code() == codes.Unavailable ||
			st.Code() == codes.DeadlineExceeded ||
			st.Code() == codes.ResourceExhausted
	}

	return false
}

// GenerateIdeaForReel generates a creative video idea for a reel without a specific track
// Used for /aidea command
func (tg *TitleGenerator) GenerateIdeaForReel(ctx context.Context) ([]string, error) {
	if tg.apiKey == "" {
		return nil, fmt.Errorf("ai: no api key configured")
	}

	// Retry strategy: 3 attempts with exponential backoff
	const maxRetries = 3
	const initialBackoff = 2 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		ideas, err := tg.generateReelIdeaWithClient(ctx)

		// Success - return immediately
		if err == nil {
			return ideas, nil
		}

		// Check if error is retryable
		isRetryable := tg.isRetryableError(err)

		// Log the error
		if attempt < maxRetries && isRetryable {
			backoff := initialBackoff * time.Duration(1<<uint(attempt-1))
			tg.log.Warnf("ai: generate reel idea failed (attempt %d/%d): %v. Retrying in %v",
				attempt, maxRetries, err, backoff)

			// Wait with backoff before retry
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
			}
			continue
		}

		// Last attempt or non-retryable error
		if attempt == maxRetries && isRetryable {
			return nil, fmt.Errorf("all %d retry attempts exhausted: %w", maxRetries, err)
		}

		// Non-retryable error on any attempt
		return nil, fmt.Errorf("generate reel idea failed: %w", err)
	}

	return nil, fmt.Errorf("generate reel idea failed: unknown error")
}

// generateReelIdeaWithClient makes the actual API call for reel idea generation
func (tg *TitleGenerator) generateReelIdeaWithClient(ctx context.Context) ([]string, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  tg.apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("genai client: %w", err)
	}

	prompt := "Роль: Ты — профессиональный Арт-директор и видеогенератор идей для Reels и TikTok.\n\n" +
		"Твоя задача:\n" +
		"1. Придумай одну оригинальную, визуально привлекательную концепцию для 12-секундного видео (рилса).\n" +
		"2. Концепция должна быть универсальной, без привязки к конкретной музыке, но с учетом, что видео может быть под любую музыку.\n" +
		"3. Предложи детальный промпт на английском языке для генерации видео в ИИ.\n\n" +
		"Требования:\n" +
		"- Общий стиль: Cinematic, Minimalist, Aesthetic, или Trendy\n" +
		"- Палитра: интересная, но не резкая, гармоничная\n" +
		"- Кадр: Macro-shot, Close-up, или средний план\n" +
		"- Движение: Плавное, медленное или динамичное, но с чёткими и выразительными переходами между сценами\n" +
		"- Фокус: Хорошая композиция, привлекающая внимание\n" +
		"- Структура видео: 3-4 сцены на 12 секунд с резкими монтажными переходами, которые делают видео удобным для зацикливания\n" +
		"- Зацикливаемость: Концовка должна легко переходить в начало, повторяя визуальную тему и ритм\n\n" +
		"Формат ответа (строго соблюдай структуру):\n" +
		"[ВАЙБ]\n" +
		"[краткое описание вайба]\n\n" +
		"[ИДЕЯ]\n" +
		"[краткая концепция видео]\n\n" +
		"[ПРОМПТ]\n" +
		"[готовый промпт на английском языке для ИИ-генерации видео]"

	resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash", []*genai.Content{
		genai.NewContentFromText(prompt, genai.RoleUser),
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("generate content: %w", err)
	}

	content := resp.Text()
	if content == "" {
		return nil, fmt.Errorf("empty response from gemini api")
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

// getFallbackIdeas returns hardcoded fallback ideas for when API is unavailable
func (tg *TitleGenerator) getFallbackIdeas(song *model.Song) []string {
	return []string{
		"[ВАЙБ]\nАтмосферный трек '" + song.Title + "' с гипнотичным, медитативным настроением, где всё держится на мягком ритме и воздушной фактуре.",
		"[ИДЕЯ]\n1. Макро-съёмка винила, иглы и лёгкой пыли в луче тёплого света, чтобы подчеркнуть ощущение живого звука.\n2. Медленные крупные планы аудиотехники, ручек микшера и тёплых отражений на металле — очень близкая, почти осязаемая студийная атмосфера.\n3. Абстрактная визуализация звуковых волн через мягкие тени, стекло и дымку, чтобы сохранить музыкальный, но не буквальный образ.\n4. Финальный атмосферный кадр с мягким уходом камеры в свет и лёгкий туман, чтобы завершить 12-секундную историю.",
		"[ПРОМПТ]\nExtreme close-up of a vinyl record, turntable needle, and subtle dust particles floating in a warm amber light beam, designed as a 12-second video with 3-4 slow scenes and sharp transitions, soft bokeh background, cinematic minimalist aesthetic, gentle camera drift, soft focus edges, atmospheric shadows, seamless loop-ready ending matching the opening mood, 4K, elegant and hypnotic mood.",
	}
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
