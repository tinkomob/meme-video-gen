package internal

import "testing"

func TestPostsChatID(t *testing.T) {
	for _, key := range []string{"TELEGRAM_BOT_TOKEN", "S3_ENDPOINT", "S3_REGION", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_ACCESS_KEY"} {
		t.Setenv(key, "test")
	}
	for _, tc := range []struct {
		canonical, legacy string
		want              int64
	}{
		{"-100123", "", -100123}, {"", "-100456", -100456}, {"123", "456", 123},
	} {
		t.Setenv("POSTS_CHAT_ID", tc.canonical)
		t.Setenv("POSTS_CHATID", tc.legacy)
		cfg, err := LoadConfig()
		if err != nil || cfg.PostsChatID != tc.want {
			t.Fatalf("posts chat=%d err=%v want=%d", cfg.PostsChatID, err, tc.want)
		}
	}
}
