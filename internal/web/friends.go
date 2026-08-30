package web

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"meme-video-gen/internal/friends"
	"meme-video-gen/internal/logging"
)

type FriendsHandler struct {
	service *friends.Service
	log     *logging.Logger
}

//go:embed friends_logo.png
var friendsLogo []byte

func NewFriendsHandler(service *friends.Service, log *logging.Logger) *FriendsHandler {
	return &FriendsHandler{service: service, log: log}
}

func (h *FriendsHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /friends", h.page)
	mux.HandleFunc("GET /friends/logo.png", h.logo)
	mux.HandleFunc("POST /api/friends/random", h.random)
	mux.HandleFunc("GET /api/friends/episode/{id}", h.episode)
	mux.HandleFunc("GET /api/friends/video/{id}", h.video)
}

func (h *FriendsHandler) page(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := strings.NewReplacer(
		"</style>", ".brand{margin:0;width:min(100%,760px)}.brand img{display:block;width:100%;height:auto;mix-blend-mode:screen}</style>",
		"<h1>Друзья</h1>", `<h1 class="brand"><img src="/friends/logo.png" alt="Друзья"></h1>`,
		"случайная серия · сезон 1", "случайная серия · все сезоны",
	).Replace(friendsPage)
	if start, end := strings.Index(page, "<script>"), strings.LastIndex(page, "</script>"); start >= 0 && end > start {
		page = page[:start] + friendsScript + page[end+len("</script>"):]
	}
	_, _ = io.WriteString(w, page)
}

func (h *FriendsHandler) logo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(friendsLogo)
}

func (h *FriendsHandler) random(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Excluded []string `json:"excluded"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		h.error(w, http.StatusBadRequest, "Некорректная история просмотра")
		return
	}
	episode, err := h.service.Random(r.Context(), request.Excluded)
	if err != nil {
		h.log.Errorf("friends random: %v", err)
		h.error(w, http.StatusServiceUnavailable, "Серия пока недоступна")
		return
	}
	writeJSON(w, http.StatusOK, episodeResponse(episode))
}

func (h *FriendsHandler) episode(w http.ResponseWriter, r *http.Request) {
	episode, found, err := h.service.EpisodeByID(r.Context(), r.PathValue("id"))
	if err != nil {
		h.log.Errorf("friends episode lookup: %v", err)
		h.error(w, http.StatusServiceUnavailable, "Серия временно недоступна")
		return
	}
	if !found {
		h.error(w, http.StatusNotFound, "Серия не найдена")
		return
	}
	writeJSON(w, http.StatusOK, episodeResponse(episode))
}

func episodeResponse(episode friends.Episode) map[string]any {
	return map[string]any{
		"id": episode.ID(), "season": episode.SeasonNumber, "episode": episode.EpisodeNumberInSeason,
		"title_ru": episode.TitleRU, "video_url": "/api/friends/video/" + episode.ID(),
	}
}

func (h *FriendsHandler) video(w http.ResponseWriter, r *http.Request) {
	episode, found, err := h.service.EpisodeByID(r.Context(), r.PathValue("id"))
	if err != nil {
		h.log.Errorf("friends video lookup: %v", err)
		h.error(w, http.StatusServiceUnavailable, "Видео временно недоступно")
		return
	}
	if !found {
		h.error(w, http.StatusNotFound, "Серия не найдена")
		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Accept-Ranges", "bytes")
	start, end, partial, err := parseRange(r.Header.Get("Range"), episode.VideoSize)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", episode.VideoSize))
		h.error(w, http.StatusRequestedRangeNotSatisfiable, "Недопустимый диапазон видео")
		return
	}
	var reader io.ReadCloser
	if partial {
		object, openErr := h.service.OpenRange(r.Context(), episode, start, end)
		if openErr != nil {
			h.log.Errorf("friends range stream: %v", openErr)
			h.error(w, http.StatusBadGateway, "Не удалось открыть видео")
			return
		}
		reader = object.Reader
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, episode.VideoSize))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		object, openErr := h.service.Open(r.Context(), episode)
		if openErr != nil {
			h.log.Errorf("friends full stream: %v", openErr)
			h.error(w, http.StatusBadGateway, "Не удалось открыть видео")
			return
		}
		reader = object.Reader
		w.Header().Set("Content-Length", strconv.FormatInt(episode.VideoSize, 10))
	}
	defer reader.Close()
	_, _ = io.Copy(w, reader)
}

func (h *FriendsHandler) error(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func parseRange(header string, size int64) (start, end int64, partial bool, err error) {
	if header == "" {
		return 0, size - 1, false, nil
	}
	if size <= 0 || !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") {
		return 0, 0, false, errors.New("invalid range")
	}
	parts := strings.Split(strings.TrimPrefix(header, "bytes="), "-")
	if len(parts) != 2 {
		return 0, 0, false, errors.New("invalid range")
	}
	if parts[0] == "" {
		count, convErr := strconv.ParseInt(parts[1], 10, 64)
		if convErr != nil || count <= 0 {
			return 0, 0, false, errors.New("invalid suffix")
		}
		if count > size {
			count = size
		}
		return size - count, size - 1, true, nil
	}
	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false, errors.New("invalid start")
	}
	end = size - 1
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, false, errors.New("invalid end")
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true, nil
}

const friendsScript = `<script>
const historyKey='friends-recent-episodes',lastWatchKey='friends-last-watch',nearEndSeconds=60;
const button=document.querySelector('#random'),status=document.querySelector('#status'),player=document.querySelector('#player'),video=document.querySelector('#video');
let currentEpisode=null,pendingStart=0,pendingAutoplay=false,lastSavedPosition=-1,loadVersion=0;
const readHistory=()=>{try{const value=JSON.parse(localStorage.getItem(historyKey)||'[]');return Array.isArray(value)?value.slice(-50):[]}catch{return[]}};
const rememberEpisode=id=>{const history=readHistory().filter(item=>item!==id);history.push(id);localStorage.setItem(historyKey,JSON.stringify(history.slice(-50)))};
const readLastWatch=()=>{try{const value=JSON.parse(localStorage.getItem(lastWatchKey)||'null');return value&&typeof value.id==='string'&&Number.isFinite(value.position)?value:null}catch{return null}};
const savePosition=()=>{if(!currentEpisode)return;const position=Math.max(0,Math.floor(video.currentTime||0));localStorage.setItem(lastWatchKey,JSON.stringify({id:currentEpisode.id,position,updatedAt:Date.now()}));lastSavedPosition=position};
const showEpisode=(episode,startAt=0,autoplay=false)=>{video.pause();currentEpisode=episode;pendingStart=Math.max(0,startAt);pendingAutoplay=autoplay;lastSavedPosition=Math.floor(pendingStart);localStorage.setItem(lastWatchKey,JSON.stringify({id:episode.id,position:lastSavedPosition,updatedAt:Date.now()}));loadVersion++;video.src=episode.video_url;document.querySelector('#number').textContent='Сезон '+episode.season+' · Серия '+episode.episode;document.querySelector('#title').textContent=episode.title_ru;player.hidden=false;video.load()};
const chooseRandom=async(autoplay=true)=>{button.disabled=true;status.textContent='Выбираем серию…';try{const response=await fetch('/api/friends/random',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({excluded:readHistory()})});const episode=await response.json();if(!response.ok)throw Error(episode.error||'Не удалось выбрать серию');rememberEpisode(episode.id);showEpisode(episode,0,autoplay);status.textContent=''}catch(error){status.textContent=error.message}finally{button.disabled=false}};
button.addEventListener('click',()=>chooseRandom(true));
video.addEventListener('loadedmetadata',async()=>{const version=loadVersion;const duration=video.duration;if(pendingStart>0&&Number.isFinite(duration)&&duration-pendingStart<=nearEndSeconds){localStorage.removeItem(lastWatchKey);await chooseRandom(true);return}if(pendingStart>0&&Number.isFinite(duration))video.currentTime=Math.min(pendingStart,Math.max(0,duration-1));if(pendingAutoplay){try{await video.play();if(version===loadVersion)status.textContent=''}catch{if(version===loadVersion)status.textContent='Нажмите воспроизведение, если браузер заблокировал автозапуск.'}}});
video.addEventListener('timeupdate',()=>{if(Math.abs((video.currentTime||0)-lastSavedPosition)>=5)savePosition()});
video.addEventListener('pause',savePosition);window.addEventListener('pagehide',savePosition);
video.addEventListener('ended',async()=>{localStorage.removeItem(lastWatchKey);await chooseRandom(true)});
(async()=>{const last=readLastWatch();if(!last)return;try{const response=await fetch('/api/friends/episode/'+encodeURIComponent(last.id));if(!response.ok)throw Error();const episode=await response.json();showEpisode(episode,last.position,false);status.textContent='Продолжите просмотр с сохранённой позиции.'}catch{localStorage.removeItem(lastWatchKey)}})();
</script>`

const friendsPage = `<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Друзья — случайная серия</title><style>
:root{color-scheme:dark;--ink:#151425;--violet:#6255be;--pink:#e7a6c8;--yellow:#f8d461;--paper:#f6f1e8}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:radial-gradient(circle at 15% 20%,#423985 0,transparent 32rem),radial-gradient(circle at 90% 85%,#d2779f55 0,transparent 28rem),var(--ink);font-family:system-ui,-apple-system,sans-serif;color:var(--paper)}main{width:min(900px,calc(100% - 32px));padding:48px 0}h1{font:700 clamp(2.8rem,9vw,6.5rem)/.82 Georgia,serif;letter-spacing:-.07em;margin:0;color:var(--yellow)}.subtitle{margin:20px 0 40px;letter-spacing:.11em;text-transform:uppercase;font-size:.72rem;color:#d9d1e5}.card{padding:clamp(18px,4vw,36px);background:#292541cc;border:1px solid #ffffff26;box-shadow:15px 17px 0 #0002}.action{background:var(--yellow);border:0;padding:15px 21px;font-weight:800;color:#25213b;font-size:1rem;cursor:pointer;box-shadow:5px 5px 0 var(--pink)}.action:focus-visible{outline:3px solid white;outline-offset:4px}.action:disabled{opacity:.65;cursor:wait}.player{margin-top:30px}.player[hidden]{display:none}video{display:block;width:100%;background:#090811;aspect-ratio:16/9}.meta{border-left:5px solid var(--pink);padding:15px 18px;background:#17152a}.meta p{margin:0;color:#cfc8dc;font-size:.82rem;text-transform:uppercase;letter-spacing:.08em}.meta h2{margin:7px 0 0;font:700 clamp(1.25rem,4vw,2rem)/1.05 Georgia,serif}.status{min-height:1.5em;margin:18px 0 0;color:#f1cedb}@media (prefers-reduced-motion:no-preference){.card{animation:arrive .35s ease-out}@keyframes arrive{from{opacity:0;transform:translateY(10px)}to{opacity:1;transform:none}}}</style></head><body><main><h1>Друзья</h1><p class="subtitle">случайная серия · сезон 1</p><section class="card"><button class="action" id="random" type="button">Случайная серия</button><p class="status" id="status" aria-live="polite"></p><div class="player" id="player" hidden><video id="video" controls preload="metadata"></video><div class="meta"><p id="number"></p><h2 id="title"></h2></div></div></section></main><script>const key='friends-recent-episodes';const read=()=>{try{const v=JSON.parse(localStorage.getItem(key)||'[]');return Array.isArray(v)?v.slice(-50):[]}catch{return[]}};const save=id=>{const v=read().filter(x=>x!==id);v.push(id);localStorage.setItem(key,JSON.stringify(v.slice(-50)))};const button=document.querySelector('#random'),status=document.querySelector('#status'),player=document.querySelector('#player'),video=document.querySelector('#video');button.onclick=async()=>{button.disabled=true;status.textContent='Выбираем серию…';try{const r=await fetch('/api/friends/random',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({excluded:read()})});const data=await r.json();if(!r.ok)throw Error(data.error||'Не удалось выбрать серию');video.pause();video.src=data.video_url;document.querySelector('#number').textContent='Сезон '+data.season+' · Серия '+data.episode;document.querySelector('#title').textContent=data.title_ru;player.hidden=false;save(data.id);status.textContent='';video.load()}catch(e){status.textContent=e.message}finally{button.disabled=false}};</script></body></html>`
