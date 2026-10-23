# AGENTS.md

FINANIKA — a Goravel (Laravel-style Go) app: JWT auth + Casbin RBAC, a live
market-data JSON API, a Gemini-powered chat advisor, and one server-rendered dashboard.
No git repository is initialised; do not propose commits, branches, or PRs.

Framework documentation lives in the local skill `.agents/skills/goravel-development/SKILL.md`
(also loadable as the `goravel-development` skill). Read it for generic Goravel/Laravel
conventions instead of guessing; this file only covers what is specific to this repo.

## Commands

```powershell
go run .                      # serve on http://127.0.0.1:3000 (APP_PORT)
go run . artisan migrate      # apply migrations
go run . artisan make:migration create_x_table   # auto-registers in bootstrap/migrations.go
go build ./... ; go vet ./... # the verification pair used in this repo
go test ./...                 # whole suite
go test ./tests/feature -run TestExampleTestSuite   # single suite
```

`artisan` / `artisan.ps1` / `artisan.bat` are thin wrappers around `go run . artisan "$@"`;
there is no standalone binary. `.air.toml` builds into `tmp/main.exe` for hot reload.

Do **not** run a blanket `gofmt -w .`. These scaffold files are intentionally left
unformatted and will show up in `gofmt -l` forever: all of `app/facades/*.go`,
`app/http/controllers/user_controller.go`, `config/{ai,database,grpc,http,telemetry}.go`,
`routes/grpc.go`, `bootstrap/providers.go`. Only format files you actually edit.

## Traps that will waste your time

- **The module path is doubled on purpose**: `edukasi-finansial-goedukasi-finansial-go`.
  Every import uses it. It is not a typo to be "fixed".
- **`binding:"..."` struct tags are silently ignored.** `goravel/gin` calls
  `gin.DisableBindValidation()` at boot (`route.go`). Validate with
  `ctx.Request().Validate(rules)` — use the shared `validate()` helper in
  `auth_controller.go`, which returns `(errors, ok)` and renders 422. Rule names that
  exist: `required`, `email`, `integer`, `numeric`, `gte`, `lte`, `min_len`.
- **`Query().First()` returns `nil` when no row matches.** The framework swallows
  `gorm.ErrRecordNotFound` (`database/gorm/query.go`). Use `FirstOrFail()` when you need
  a distinguishable error, or check the destination's zero value. Reading `err == nil`
  as "row exists" is a bug.
- **`os.Getenv` never sees `.env`.** Goravel loads `.env` into viper and never calls
  `os.Setenv`, so `os.Getenv("GEMINI_API_KEY")` is always empty. Always go through
  `config.Env(...)` in `config/*.go` or `facades.Config().GetString(...)`. Real OS
  environment variables do work and take precedence over the file.
- **Views are parsed once at boot.** `goravel/gin` walks `resources/views` and calls
  `ParseFiles` during startup, so editing any view requires a server restart.
- **ApexCharts' `height: '100%'` reads the *parent* box, not the chart div.** `drawChart`
  always sets it, which is fine where the container is a flex item with a definite height
  (every list|chart page), but a chart inside a plain card renders at the parent's inner height
  and visibly overflows its own `h-[Npx]` container. That is why the modal chart is wrapped in
  `<div class="mt-4 h-[300px]">` with `#pasar-detail-chart` at `h-full` — give any new chart
  container a parent with a real height. The Dasbor chart (`#market-chart`, `h-[320px]` in an
  auto-height card) still renders ~87 px taller than its div; it is pre-existing and was left
  alone on purpose, so do not "fix" it while touching something else.
- **Every file under `resources/views` is a Go template with `{{ }}` delimiters.** A
  stray `{{` in CSS or JS breaks the whole boot. The callable name is the `{{ define "x" }}`
  name if present, otherwise the base filename — `dashboard.html` has no `define`, so it
  is rendered as `"dashboard.html"`.
- **A global 3-second request-timeout middleware** comes from `http.request_timeout`.
  Any slower handler needs `HTTP_REQUEST_TIMEOUT` raised in `.env` (currently `90`; the
  HTTP client timeout is `60s`). This is why the chat endpoint would 408 without it.
- **`go run .` leaves an orphan child process holding port 3000** after the wrapper exits
  (you may see `exit status 0xffffffff` while the server keeps serving, or the reverse).
  Kill it by port, not by name:
  ```powershell
  Get-NetTCPConnection -LocalPort 3000 -State Listen | ForEach-Object { Stop-Process -Id $_.OwningProcess -Force }
  ```
  The running process shows up as `edukasi-finansial-goedukasi-finansial-go`.

## Wiring

`main.go` → `bootstrap.Boot()` → `bootstrap/app.go`, which registers migrations, routes
(`routes.Web()`, `routes.Api()`, `routes.Grpc()`), service providers, and config.
`routes/web.go` serves HTML/static, `routes/api.go` serves the JSON API under `/api`.
`GET /platform` (`DashboardController.Platform`) exists only to 302 to `/#/platform`: the app
serves one view, so a new page is a `data-panel` and its address is the hash, never a second
route with its own copy of the sidebar, topbar, and script.

```
app/http/controllers/   auth, profile, market, chat, risk, portfolio, dashboard controllers
app/http/middleware/    auth_middleware.go, role_middleware.go
app/models/             User, ChatMessage, RiskProfile, Portfolio (all embed orm.Model)
app/services/           market_service, ai_service, insight_service, world_asset_service, casbin
app/facades/            generated facade wrappers — import these, not the framework's
config/                 one file per config key; casbin_model.conf + casbin_policy.csv live here
bootstrap/migrations.go the migration registry (make:migration updates it for you)
```

Middleware must be a struct with `Signature() string` and `Handle(http.Context)`, then be
attached with `route.Middleware(...)`. `AuthMiddleware` decodes the JWT itself and stashes
`user_id` / `user_role` in the request context; read the id via `currentUserID(ctx)`.

## Auth & RBAC

JWT is hand-rolled with `golang-jwt/v5` + `bcrypt` (`auth_controller.go`) — **not** the
framework's auth guard. `JWT_SECRET` / `JWT_TTL` come from `config/jwt.go`; claims are
`sub`, `role`, `iat`, `exp` with HS256, and the middleware pins the algorithm.

Casbin is used directly through `github.com/casbin/casbin/v2`; **`github.com/goravel/casbin`
does not exist**, so do not try to install it. The enforcer is a `sync.Once` singleton in
`app/services/casbin.go` built from `config/casbin_model.conf` + `config/casbin_policy.csv`.
`RoleMiddleware` grants access when the route explicitly lists the role **or** when Casbin
allows `(role, path, method)`. Because the enforcer is cached in-process, policy file
changes need a restart.

New users default to role `user`; self-registration cannot create admins. Promote by
updating the `users.role` column directly or from Panel Developer. The seeded developer
account is `admin@finanika.com` (`database/seeders/admin_user_seeder.go`) and the regular test
account is `user@finanika.com` (`database/seeders/regular_user_seeder.go`); both passwords come
from the seeders, so change them outside source control for any real deployment. Both seeders
go through `upsertSeedUser` (`user_account_seeder.go`), which hashes with bcrypt on every run and
updates an existing row, so `go run . artisan db:seed --seeder=AdminUserSeeder` is the way to
repair a login after a database restore.

## Profile

`users.gender`, `users.avatar_url`, and `users.bio` come from
`20261020000003_add_user_profile_fields.go`; all three default to an empty string so old
accounts stay valid. `PUT /api/profile` (auth) accepts JSON or multipart, never accepts an
email from the body, and normalises gender to `Laki-laki` / `Perempuan` (blank is allowed).
The bio is capped at 100 runes. An avatar is sniffed with `http.DetectContentType` (JPG,
PNG, or WEBP only, max 2 MB), written under `public/uploads/avatars/`, and served through
the existing `/public` static route. The topbar name/avatar and the sidebar account block
both open `data-panel="profil"`; the email field there is read-only. Developer Panel's user
table shows the gender column and a `Lihat Biodata` modal with the avatar, gender, role, and
bio. `userResource` and `adminUserRow` must both keep exposing the three new fields.

## Admin content

Panel Developer (`data-panel="admin"`, `AdminController` + `AdminContentController`) manages
users and the content behind the student pages. All routes are inside the existing
`AuthMiddleware` + `RoleMiddleware("admin")` group; regular users get a `403`:

| Route | Purpose |
| --- | --- |
| `GET /api/admin/dashboard` | counts for the stat cards |
| `GET/POST /api/admin/modules`, `PUT/DELETE /api/admin/modules/{id}` | module + slide CRUD |
| `GET/POST /api/admin/quizzes`, `PUT/DELETE /api/admin/quizzes/{id}` | quiz CRUD |
| `GET/POST /api/admin/platforms`, `PUT/DELETE /api/admin/platforms/{id}` | platform CRUD |

The schema changes are in `20261020000002_create_admin_content_tables.go`:
`education_modules.icon`, `education_quizzes.option_d` / `explanation`, and the new
`investment_platforms` table. Modules keep their slides in `content` as JSON through
`models.SlidesToJSON`; deleting a module relies on the existing cascading foreign keys to
remove its quizzes and `user_module_progress` rows. Quiz answers are validated as `a`–`d`
and all four options plus an explanation are required. Platform pros/cons are JSON arrays
with at least three points per side, and both sides must contain one cost word
(`fee`/`biaya`/`spread`/`komisi`/`admin`). The admin forms are one reused `#admin-modal`
in the view; table rows are delegated listeners, so the lists can be re-rendered safely.
The whole panel follows the topbar language: static labels come from `TEXT_I18N_EXTRA`,
JS-built table rows/statuses/modal labels from `TEXT_I18N_DYNAMIC` and the admin patterns
(`{n} modul`, `Slide {n}`, `Poin {n}`, role-change/delete confirmations), so a new admin
string needs an entry in both places or it stays Indonesian in EN mode.

`EducationController` serves **three random questions per attempt** (`quizQuestionLimit`)
from the module's bank and shuffles the four options every load. The served options are
`[{id, text}]`; `id` stays the stable `a`–`d` identity while the displayed A/B/C/D letter is
only the shuffled position. `QuizSubmit` scores the submitted question ids against those
stable option ids, rejects incomplete/unknown answer sets, and computes the pass score as
`total/2 + 1`, so adding quiz questions does not leave a fixed two-answer threshold.
`EducationQuizSeeder` targets `quizTargetPerModule` (10) questions per module: it inserts
missing starter questions and backfills blank `option_d`/`explanation` fields without
rewriting existing questions or answer keys.

`EducationModuleSeeder` fills the table only when it is empty (an existing install only gets
a missing icon backfilled), and `InvestmentPlatformSeeder` adds only names that are not in
the table yet, so admin edits survive `db:seed`. `GET /api/platforms` is public because the
Rekomendasi Platform page is open to guests.

## Market data

Live prices come from three upstreams, fetched independently: CoinGecko (BTC), xaus.com
(gold), Yahoo Finance (USD/IDR). A failing source is reported under `prices.errors` instead
of failing the request; the endpoint only fails when all three fail.

- **Yahoo Finance answers 429 without a browser-like `User-Agent`** — `fetchJson` sets one.
  Keep it when touching that code.
- Results are cached 15 minutes with `facades.Cache().Remember` on the memory driver, so
  the cache dies on restart and the first call is slow. Cache keys are prefixed with
  `APP_NAME`.
- **xaus.com sends no daily comparison for gold**, so `fetchGold` borrows the 24-hour
  percentage from Yahoo `GC=F` (`yahooGoldSymbol`) as a best-effort extra: when that call
  fails the gold card still renders its prices and only the `change_percent` badge is empty.
- `toMap` / `toSlice` / `toFloat64` / `toStr` are package-level helpers in `app/services`
  shared by the market and AI services — reuse them rather than adding new copies.
- `fetchJson` decodes a JSON *object*; CoinGecko's `/coins/markets` answers with a top-level
  *array*, so it goes through `fetchJsonArray` (same headers/U-A/error handling).
- `GET /api/crypto` (protected) lists the ten coins on the Crypto page from CoinGecko
  `/coins/markets` — `{"data": [{id, symbol, name, image, price_usd, change_24h, market_cap}]}`.
  The ten ids live in the `cryptoCoins` slice and `coinGeckoMarketsURL()` builds the URL from
  it; the API returns them market-cap desc, which is not the order they are listed in the URL.
  Sui replaced Dogecoin, so `dogecoin` is no longer a valid `asset` anywhere.
- `GET /api/forex` (protected) returns `{"data": [{code, name, pair, rate, change_percent,
  previous_close, source}]}` for USD, EUR, JPY, GBP, SGD, CNY, AUD, CAD, CHF, HKD, all quoted
  against the rupiah. The set is the ten most-traded currencies in the BIS survey; the order in
  `forexCurrencies` is also the order the Kurs page cycles through with its Sebelumnya/Selanjutnya
  buttons, so appending is safe and reordering changes the navigation. The ten Yahoo requests run
  concurrently in `fetchForexRates` but take a slot from a `yahooQuoteWorkers` (6) channel — ten
  at once get some pairs answered with 429; a single missing pair is logged and skipped, and only
  an all-fail is a 502. `fetchYahooIdrRate` is shared with the USD/IDR card in `/api/market`.
- `GET /api/saham` (protected) serves the twenty rows on the Saham page — ten per tab. The
  Indonesian side is `^JKSE` IHSG plus `BBCA.JK`, `BBRI.JK`, `TLKM.JK`, `ASII.JK`, `BMRI.JK`,
  `UNVR.JK`, `ICBP.JK`, `INDF.JK`, `KLBF.JK`, all quoted in rupiah; the foreign side is
  `^IXIC` NASDAQ Composite, `^GSPC` S&P 500, `^DJI` Dow Jones and `AAPL`, `MSFT`, `GOOGL`,
  `AMZN`, `TSLA`, `NVDA`, `META`. `GET /api/komoditas` (protected) serves the twenty futures —
  `GC=F` emas, `SI=F` perak, `CL=F` minyak WTI, `NG=F` gas, `HG=F` tembaga, `KC=F` biji kopi
  arabika, `ZW=F` gandum, `ZC=F` jagung, `ZS=F` kedelai, `MTF=F` batu bara, `CPO=F` minyak sawit,
  `CC=F` kakao, `ZR=F` beras, `SB=F` gula, `ALI=F` aluminium, `TIO=F` bijih besi — plus four
  Indonesian food commodities from Bank Indonesia PIHPS: cabai merah (`com_13`, asset `chili`),
  bawang merah (`com_11`, `shallot`), bawang putih (`com_12`, `garlic`), and cabai rawit (`com_16`,
  `rawit`). Shape is
  `{"data": [{symbol, name, price, change_percent, previous_close, currency, source}]}`, plus a
  `unit` field on commodities, an `asset` field on the PIHPS rows, and a `market` field
  (`id` / `us`) on stocks. The tables are `sahamInstruments` / `komoditasInstruments`, both
  `[]yahooInstrument`; `fetchYahooQuotes` queries the Yahoo half and drops only the symbols that
  failed, while `fetchKomoditasQuotes` merges in the four PIHPS rows.
  **That helper caps concurrency at `yahooQuoteWorkers` (6).** Twenty simultaneous requests to
  Yahoo get some symbols answered with 429 and the row is then silently missing, so the
  goroutines take a slot from a buffered channel; a bigger instrument list only means more
  waves, not more parallel load.
  **The four food rows have no Yahoo ticker.** `yahooInstrument.PIHPSID` marks them and
  `fetchPihpsQuotes` fetches them with four workers (`pihpsChunkWorkers`) from BI's
  `GetGridDataKomoditas` endpoint, which is also why `fetchPihpsJson` sends a browser
  `User-Agent`, a `Referer` to `bi.go.id/hargapangan`, and `X-Requested-With`. The first row
  (`Semua Provinsi`) is the national average; a `-` cell means no data. Prices are IDR per
  kilogram (`unit: "per kg"`, `currency: "IDR"`, `source: "pihps"`), so the view must format them
  with `moneyIdr`/`commodityValue`, never a USD conversion. `change_percent` is computed from
  the latest two `price` points. There is no `StaticPrice`/`StaticChange` branch and no
  "estimasi" badge anywhere any more: all twenty rows are live quotes.
  **`name` and `unit` come from our own table, not from Yahoo** — Yahoo names futures after the
  contract month ("Silver Dec 26"). Emas is in this list as the COMEX future `GC=F`, so its row
  can differ slightly from the xaus.com spot price on the dashboard card.
  **Yahoo quotes coffee and the grains in US cents** (`meta.currency == "USX"`, e.g. `KC=F`
  305.55), so `yahooPriceScale` divides those by 100 — otherwise the table would print
  "$305,56 per pon". The same scale is applied to the daily history series
  (`yahooMetaCurrency`), so the chart matches the table.
  **`MTF=F` stopped updating on Yahoo**: the chart still answers with timestamps but its
  `close` array has been null since 26 Dec 2025, so the coal row's price is stale and the
  series ends mid-2025. `chartDomain(days, data)` un-pins the X axis in that case, otherwise
  the pinned "now − days … now" window would leave the coal chart empty.
  `/api/saham` was `/api/saham-us` before Fase 4; the route, the controller method (`Saham`),
  the cache key (`market_saham`) and the table name all changed together.
- All four listings (crypto, forex, saham, komoditas) share `cachedQuotes` for the 15-minute
  cache, so a new listing should go through it rather than repeating the `Remember` block.
- These endpoints return 502 with a friendly `error` when upstream fails, mirroring the
  history endpoint.
- Every Yahoo quote — kurs, indeks, komoditas — comes from one shape, `yahooQuoteURL`
  (`/v8/finance/chart/%s?interval=1d&range=1d`), read by `fetchYahooQuote` from the chart's
  `meta` block. Indices must keep their `^` (`url.PathEscape` handles it; the raw form works
  too). The live price is `regularMarketPrice`, the previous close is `chartPreviousClose`
  (not `previousClose`, which the chart meta does not carry).
- `GET /api/market/history?asset=<coin|gold|usd>&days=1..3650` (protected) serves the daily
  close series behind the dashboard charts, one value per 3H…10T button (3, 7, 30, 90, 180,
  365, 1825, 3650). `asset` is any CoinGecko id in `cryptoCoins`, plus `gold` and `usd`;
  `IsHistoryAsset` gates it and `HistoryAssetNames()` feeds the 422 message. CoinGecko
  `market_chart` covers coins up to 365 days; everything longer — and all of gold/USD —
  comes from Yahoo Finance `chart` with `interval=1d`: `GC=F` (COMEX futures, so a small
  basis gap vs the xaus.com spot price on the cards), `USDIDR=X`, and the coin's own ticker.
  `days` is clamped, a bad `asset` is a 422, and upstream failures are a friendly 502 with
  the cause in the log. The four PIHPS food assets (`chili`, `shallot`, `garlic`, `rawit`) are
  accepted by `IsHistoryAsset` and served by `fetchPihpsSeries`, which walks the range in
  `pihpsChunkDays` (30-day) windows with `pihpsChunkWorkers` (4) concurrent requests; the
  points carry `source: "pihps"`, and a failure is still a friendly 502 like every other
  history asset. `fullDailySeries` forces those assets to the long span, and `lastNDays`
  slices by point count.
- **The Yahoo ticker is not always `<symbol>-USD`.** It is stored per coin as
  `cryptoCoin.Yahoo` in `cryptoCoins` because Sui only exists as `SUI20947-USD` (`SUI-USD` is
  delisted). One slice is the source for the markets URL, the history whitelist and the
  long-range ticker, so adding a coin means adding its Yahoo ticker in the same place.
- **CoinGecko's anonymous tier rejects `days` above 365 with 401, not 429** (`days=max`
  included). Every coin's 5T/10T therefore switch source to Yahoo, which is why those two
  ranges can differ a little from the 1T line where they overlap. A Pro key would lift that;
  the URL is `coinGeckoHistoryURL` (a `%s` format taking the coin id).
- **The `source` field is not a static property of the range — it is what actually served the
  series.** `GetHistoricalPrices` returns `(points, source)` and `fetchDailySeries` reports it,
  because a coin's ≤1y request that CoinGecko refuses falls back to that coin's Yahoo ticker:
  the chart still draws and the response says `yahoo_finance` instead of lying with
  `coingecko`. There is no `HistorySource(asset, days)` helper any more; do not reintroduce a
  predicted source. The `historySeries` struct carried through both cache layers is what keeps
  `source` attached to the data it describes.
- **History fetches one series per asset and upstream span, not one per range.**
  `GetHistoricalPrices` caches the sliced result under `market_history_{asset}_{days}`, but
  the only upstream call is `market_history_full_{asset}_{1y|10y}` (15 min). The whole
  3H…10T sweep therefore costs one Yahoo request plus one CoinGecko request instead of
  eight. The rate limit is real: a six-range sweep with per-range fetches returns 429 by the
  fifth call. Each coin counts as its own asset, so a *cold* click-through of all ten Crypto
  rows costs ten CoinGecko requests and the anonymous tier answers **429** around the ninth or
  tenth — that is exactly what the Yahoo fallback absorbs, so the click-through now renders ten
  charts instead of a 502 (the `fetchJsonWithRetry` backoff is 3.6 s, too short for a
  per-minute window, which is why retrying alone could not fix it).
- The history path uses `fetchJsonWithRetry` (3 attempts, 1.2s/2.4s backoff) on 429/503;
  the live-price path keeps plain `fetchJson`. Yahoo daily bars are dated with the symbol's
  `meta.gmtoffset` so labels do not drift a day.
- The chart pins `xaxis.min`/`max` to `now - days*86400000` … `now` in `chartDomain()`, so
  every asset shares one date scale. Without it each asset auto-scales to its own data span
  (Bitcoin trades daily, gold and USD/IDR only on trading days) and the lines stop lining
  up. Gold/USD lines still end at their last trading day, up to a weekend short of the edge.

## AI (Chat FINANIKA)

Gemini REST at `{base_url}/models/{model}:generateContent`, authenticated with the
`x-goog-api-key` header, sending `systemInstruction` + `contents` + `generationConfig`.
The reply text is `candidates[0].content.parts[].text`.

- **`gemini-1.5-flash` and `gemini-2.5-flash` are rejected for new API keys.** The default
  is `gemini-3.8-flash`, overridable via `GEMINI_MODEL`, set in four places
  (`config/gemini.go`, `defaultGeminiModel` in `ai_service.go`, `.env`, `.env.example`) —
  keep them in sync. If a call returns 404, check which models the key may actually use
  with `GET https://generativelanguage.googleapis.com/v1beta/models`.
- An empty key or the literal placeholder string (`geminiApiKeyPlaceholder` in
  `ai_service.go`) is treated as "not configured", so users get a clear message instead of
  Google's 400.
- **Answers are plain text, and that takes two layers.** `plainTextInstruction`
  (`ai_service.go`) is appended to all three system prompts — `systemInstruction`,
  `assetAnalysisInstruction`, `newsInstruction` — and explicitly forbids `**tebal**`,
  `*miring*`, `#` headings and inline code, asking for lists as `"- "` lines instead. Models
  still slip markers in occasionally, and a narration cached before this rule existed keeps
  them for up to 30 minutes, so `dashboard.html` also runs `cleanMarkdown()` over every AI text
  it renders: it drops heading/emphasis/code markers, deletes `---` rules and turns `"- "` lines
  into `"• "` bullets (that is the "rapi" bullet the brief asked for). `cleanMarkdown()` rewrites
  the *string* only, so callers keep using `textContent` — never move those assignments to
  `innerHTML`, because the text comes from a provider and from the user's own question.
- The prompt persona and the per-risk-profile DCA rules live in `systemInstruction()`.
  It also carries the **off-topic rule**: questions outside finance/economics/investing
  (recipes, films, sports) must be refused politely and redirected, which is why
  `GenerateNarasi` takes the user's question as an argument — without it the
  model only ever saw the fixed market-summary prompt.
- **Every AI answer closes with a `Saran:` line, not with the old disclaimer** (Fase 14).
  `penutupInstruction` (`ai_service.go`) is appended to all three prompt builders next to
  `plainTextInstruction`: it asks for one concrete closing line that names its reason (wait for
  the dip, raise the DCA allocation, hold more cash) and it explicitly **forbids** `"ini hanya
  edukasi"`, `"bukan nasihat keuangan profesional"` and `"bukan saran investasi"` — the
  sentence every answer used to end with. Do not put a "disclaimer" bullet back into a prompt.
  The chat panel localizes that label when the topbar language is English: `chatLanguages`
  carries `Suggestion:` and `chatUserPrompt` uses it, so an English user does not get an
  Indonesian closing label. The dashboard summary card stays Indonesian.
- **The latest Insight Global headlines travel with the prompt.** `formatNewsDigest()` renders
  the newest `aiNewsDigestLimit` (5) news as plain `1. [Kategori] Judul` lines. The controllers
  fetch them with `InsightService.List()` — a one-hour cache, so this is not a new network call
  per question — and pass them down; `systemInstruction` then asks the model to tie today's
  price moves to one of those headlines and forbids inventing details that are not in the list.
  Only the titles are sent: an RSS item's description is just the media name. Five is enough;
  a longer digest only adds tokens and latency.
- `GenerateNarasi(profilRisiko, marketData, berita, pertanyaan)` is stateless: chat history is
  persisted to `chat_messages` but **not** replayed, so the summary card does not remember
  earlier turns. It is used **only** by the Dasbor summary card; the chat panel goes through
  `GenerateChat` (below), which now replays history.
- **The chat panel keeps the last few turns so follow-up questions stay connected.** `SendMessage`
  reads `recentChatHistory(userID)` **before** storing the new user message, and
  `GenerateChat`/`GenerateChatWithImage` pass those `ChatTurn`s into `generateWithFallback`,
  which builds alternating `user`/`model` turns for Gemini and `user`/`assistant` messages for
  Groq. `cleanChatHistory` keeps the newest `ChatHistoryTurns` (6), drops invalid roles and the
  `[gambar]` placeholder, and caps each turn at 600 runes. Without this, "Kalau kekurangannya?"
  had no referent and the model answered a different topic. The history is per user and comes
  from the database, not from the browser, so it cannot be forged by a client.
- **The chat panel sends an application context block, and the server is authoritative for the
  parts it owns.** `POST /api/chat/send` accepts `{message, context}` where `context` is
  `{profile, language, prices[], news[], platforms[]}` collected by `chatContext()` in the view.
  `PrepareChatContext` (`app/services/chat_context.go`) cleans it: labels are stripped to
  letters/digits/`()/.,%-+&:` and capped (40 runes for an asset, 140 for a headline), prices
  that are NaN/Inf/≤0 are dropped, platforms are de-duplicated, and counts are capped at 12/5/40.
  Newlines are collapsed by `cleanChatText` so client text cannot break the prompt's structure.
  **Prices, profile and news are then taken from the server** — `mergeChatPrices` starts from
  `chatPricesFromMarket` and only appends client rows whose first label word is not already
  covered, and `chatNewsFromInsight` replaces the client list whenever the server has news.
  Otherwise a crafted request could put a fake BTC price or a fake "Insight Global" headline in
  the prompt. The client list that genuinely matters is **platforms**, because the platform
  list is loaded into `state.platforms` from `GET /api/platforms` rather than reconstructed
  server-side. `chatPlatformContext(message)` sends names/categories/types for all 36, but
  attaches a `description` only for the platform named in the question, and only its first
  sentence (`firstSentence`, capped again at 160 runes by `cleanChatText`). That keeps the prompt
  small while still giving the model enough detail to answer "biaya di Indodax". Risk profile
  comes from `latestRiskProfileRecord`; the client value is used only when the user has never
  taken the quiz.
- `GenerateChat(pertanyaan, chat)` builds `systemInstruction(profile) + chatContextInstruction`,
  so the persona, off-topic rule, DCA rules, `plainTextInstruction` and `penutupInstruction` are
  all inherited — do not fork the persona for chat. The context block reads
  `Konteks aplikasi FINANIKA … Profil risiko pengguna: X … Harga pasar saat ini: … Berita
  terbaru (Insight Global): … Platform yang direkomendasikan di aplikasi ini: …` followed by
  rules that forbid inventing prices/headlines and restrict platforms to the given list.
  `chatUserPrompt` takes a fallback question because the image path may arrive with no text.
- **Images travel as multipart and only Gemini can answer them.** The view downscales the file
  on a canvas to ≤1280 px / JPEG 0.85 before upload, then posts `message`, `context` (a JSON
  *string*) and `image` as `FormData`; the JSON path is unchanged when there is no image, so
  `bindChatRequest` branches on `Content-Type`. `services.NewChatImage` sniffs the MIME with
  `http.DetectContentType` (never the filename), accepts only PNG/JPEG/WEBP and caps the file at
  `ChatImageMaxBytes` (4 MB); `geminiParts` turns it into an `inline_data` part with
  `mime_type` + base64 `data`. `GenerateChatImage` is **Gemini-only on purpose** — Groq's
  `openai/gpt-oss-120b` cannot see images, so falling back would produce an answer that pretends
  it did — and it uses `aiFallbackBudget` rather than `geminiTimeout`, because there is no Groq
  leg to reserve time for. A failure there returns `aiFailureWith(…, "FINANIKA belum bisa membaca
  gambarnya")`. Images are **not** stored in `chat_messages`; an image-only message persists the
  literal `[gambar]` so the history row is never blank.
- `SendMessage` persists the user message *before* calling Gemini; on failure the user
  message is already stored and no assistant row is written.
- Every AI call goes through `AIService.GenerateWithFallback(system, user, temperature)`, which
  tries Gemini (`generate`, unexported and Gemini-only, now taking a `context.Context`) and
  switches to **Groq** if Gemini fails. Chat uses `geminiTemperature` (0.9); `AnalyzeAsset` and
  `ExplainNews` use `geminiFocusedTemperature` (0.4) because they report numbers and should not
  drift. All AI features inherit the fallback because they all enter here — `GenerateChat`,
  `GenerateNarasi`, `MarketSummary` (through `GenerateNarasi`), `AnalyzeAsset`, `ExplainNews` —
  so no controller picks a provider and a new AI feature cannot silently miss the fallback.
  The one deliberate exception is `GenerateChatImage`, which skips Groq because the image would
  be lost.
- **Groq is the fallback provider** (Fase 12): `config/groq.go` plus `GROQ_API_KEY`,
  `GROQ_MODEL`, `GROQ_BASE_URL` in `.env`/`.env.example`. It posts to
  `{base_url}/chat/completions` with an OpenAI-style body
  (`{model, messages:[{role,content}], temperature, max_tokens, reasoning_effort}`) and reads the
  reply from `choices[0].message.content` (`extractGroqText`; the `reasoning` field some gpt-oss
  models send is deliberately ignored). `max_tokens` is `groqMaxTokens` (4096) and
  `reasoning_effort` is `low`: gpt-oss can otherwise spend most of the budget "thinking" and stop
  mid-sentence, which is exactly what truncated the Insight answers. The system prompt travels as
  its own `system` message, so the FINANIKA persona, the off-topic rule and the per-profile DCA
  rules survive the switch — that is why `GenerateWithGroq` takes `system` and `user`, not the
  single `prompt` string of the brief.
- **The Groq model here is `openai/gpt-oss-120b`, not `llama-3.3-70b-versatile`.** Groq retired
  that one and answers `404 model_not_found`, so the model named in the brief would have failed
  on every fallback. Before changing `GROQ_MODEL`, list what the key may use with
  `GET {base_url}/models`.
- **`os.Getenv("GROQ_API_KEY")` does not work here** — the `.env` trap above applies to every
  key, so `config/groq.go` reads it with `config.Env(...)`; only real OS environment variables
  reach `os.Getenv`.
- The switch happens on **every** Gemini failure — 429/503, timeouts, 5xx, an unknown model,
  even an unset `GEMINI_API_KEY` (a deployment with only a Groq key still answers) — with one
  exception: `ErrAIPromptRejected` (`blockReason`), because a content decision by one provider
  must not be re-asked at another. Each switch is logged
  (`Gemini gagal (…), beralih ke Groq`).
- The chain is bounded by `aiFallbackBudget` (80s), with `geminiTimeout` (12s) reserved for the
  Gemini leg, because `HTTP_REQUEST_TIMEOUT` (90s) caps the whole request: without the budget a
  hanging Gemini plus the Groq retry would end in a 408 instead of an answer. Twelve seconds is
  enough for the Gemini calls that do answer here and keeps a first summary from waiting half a
  minute; slower calls are answered by Groq. After a Gemini failure `markGeminiCooldown()` skips
  Gemini for `geminiCooldown` (3 minutes), so the next requests go straight to Groq instead of
  waiting for the same timeout again; a successful Gemini call leaves no cooldown behind.
- **AI output caps are `geminiMaxTokens` / `groqMaxTokens` = 4096.** The earlier 2048 was too
  small once a provider counted reasoning tokens against the same budget. `logGeminiFinishReason`
  writes a `MAX_TOKENS` warning to the log, and neither `GenerateNarasi` nor the frontend slices
  the returned string: `fillParagraphs` only splits it into paragraphs, so a complete last
  sentence stays visible.
- When **both** providers fail the sentinel still wins: the user keeps getting
  `429`/`503` with the friendly "sedang sibuk" body and never the upstream JSON, while the Groq
  failure goes to the log (`cadangan Groq juga gagal: …`).
- `GET /api/ai/summary` (auth) is the "FINANIKA Financial Advisor" card on the Dasbor. It
  answers `{summary, profile, generated_at, cached, prices}`: `summary` comes from
  `AIService.MarketSummary`, which wraps `GenerateNarasi` in
  `facades.Cache().Remember(aiSummaryCacheKey(profil), aiSummaryCacheTTL, …)` — **30 minutes,
  keyed per risk profile** (`ai_market_summary_<profil>`). Opening or reloading the Dasbor
  therefore replays the same narration instead of spending another Gemini request, and users
  with the same profile share one entry. The profile is part of the key on purpose: the
  narration is assembled from it (`systemInstruction` carries the per-profile DCA rules), so one
  shared key would hand a Konservatif user the advice written for someone else. `cached` is
  `Cache().Has(key)` read *before* the `Remember` call, which is how the card can honestly say
  "Dari cache Gemini" instead of implying a fresh answer. Failures are never cached — `Remember`
  only stores a successful callback — so a 429/503 does not pin the error message for half an
  hour. The cached payload is a `map[string]any` (`summary` + `generated_at`), not a struct, so
  it survives a driver that JSON round-trips. `prices` (the same shape as `/api/market`) is
  fetched **outside** the cache, so a stale narration still ships with current quotes. The
  news digest is passed in by the controller and captured by the cache closure, so it is
  frozen with the narration: for up to 30 minutes the `Saran:` line can cite a headline that
  has since scrolled off the Insight page. That is the intended trade-off — the alternative is
  one Gemini request per Dasbor reload.
- `POST /api/ai/analyze-asset` (auth) is a per-asset analysis endpoint that **no page calls
  any more** (Fase 10 removed the Dasbor AI Analysis card; it is kept for direct API use):
  body
  `{asset, label, days, history:[{date, price}]}`, at least `analysisMinPoints` (5) usable
  points and at most `analysisMaxPoints` (400, the tail is kept). It answers
  `{asset, label, days, points, analysis}`. The series is sent *by the caller* — the same
  30 days the chart draws — so the server validates the shape instead of re-fetching.
- **`AnalyzeAsset` caches its answer one hour per asset** (`ai_analysis_<asset>`,
  `aiAnalysisCacheTTL`) through `facades.Cache().Remember`, so re-opening the same asset —
  or flipping between coins — does not spend another Gemini request. The key is the asset id
  only (not the series), which is why the answer can lag the 15-minute price cache; that is
  the intended trade-off. `Remember` never stores the result when the callback fails, so a
  rate-limited call is not cached for an hour.
- `GET /api/insight` (auth) serves the Insight Global list plus a `source` field
  (`google_news` / `contoh`). It comes from `InsightService` (`app/services/insight_service.go`),
  which fetches two Google News **RSS** queries (free, no API key — unlike GNews/NewsAPI),
  one hour cached, and falls back to `contohInsightNews` (20 hand-written items) whenever the
  feed fails or `INSIGHT_FEED=false`. `POST /api/insight/explain` (auth) turns one of them
  into an asset-impact explanation: `Find` resolves the news by `id` then `title` against the
  *currently served* list, so Gemini only ever sees our own title and summary — the endpoint
  cannot be used to make the advisor discuss arbitrary text.
- **News explanations are cached 30 minutes per article and language.** `ExplainNews` wraps
  `GenerateWithFallback` in `facades.Cache().Remember(ai_news_explanation_<lang>_<id>, …)`
  (`aiNewsCacheTTL`), so pressing "Tanya FinGo AI" again on the same card is instant; failures
  are not cached. The key falls back to a SHA-256 prefix of the title when an old caller sends
  no id. The view also keeps `state.insightAnswers[id|language]`, so a repeat click inside the
  same session does not even issue a request; `logout()` clears that map. If the id or language
  changes, the answer is fetched/cached separately.
- **The page shows `insightMaxItems` (20) cards** (Fase 14), and that one constant governs both
  paths: the merged feed result is cut to it, and the hand-written fallback list is exactly that
  long, so "Google News · 20 berita" and "Daftar contoh · 20 berita" never disagree on the count.
  Each feed query is asked for more than its even share (`insightFeedPerTopicSlack`) because the
  two topics overlap and the duplicates are dropped only after the merge — without the slack the
  list silently shrinks to the high teens. Every card carries its own "Tanya FinGo AI" button,
  because `renderInsight` builds the button per item; there is no separate wiring to add.
- The RSS `description` is only a link, so the summary is built as `Laporan <media>`; titles
  are normalised from `"Judul - Media"`, `pubDate` from RFC1123 to `YYYY-MM-DD`, and each
  item gets a `gn-<hash>` id (`newsID`) because a headline is not a usable DOM id. Categories
  are guessed from keywords (`newsCategoryKeywords`) only to colour the badge.
- **`splitNewsTitle` strips the media with the feed's `<source>` element first.** Some outlets
  are named with a dash themselves (`Bisnis.com - Market`), so cutting at the *last* `" - "` left
  `"Judul … - Bisnis.com"` as the headline. When the title ends with `" - " + source` that exact
  suffix is removed; otherwise it falls back to the old last-dash cut. That is why `rssFeed`
  reads `Source` even though nothing else uses it.
- A Gemini **503** (high demand) or **429** (quota exhausted) is NOT returned as a raw
  upstream detail: the service returns the sentinels `services.ErrAIHighDemand` /
  `services.ErrAIRateLimited`, and the controllers map them through the shared `aiFailure()`
  (`app/http/controllers/ai_controller.go`) to
  `503 {"error": "Sistem AI sedang sibuk karena banyak pengguna. Coba beberapa saat lagi ya."}`
  and `429 {"error": "Sistem AI sedang sibuk karena banyak permintaan. Mohon tunggu 1 menit lagi ya."}`,
  which the dashboard renders as a soft amber bubble. Keep those bodies free of `detail` —
  the frontend's `errorMessage()` prefers `detail` and would leak the upstream text. Every
  other upstream failure keeps the old `502 {"error", "detail"}` shape.

## Risk profile

`risk_profiles` stores one row per quiz submission; `latestRiskProfile(userID)` reads the
newest by `id desc` and falls back to `Moderat`. Thresholds live in `risk_controller.go`
(`skorMaksimalKonservatif = 10`, `skorMaksimalModerat = 15`); `skorMinimal = 5` and
`skorMaksimal = 20` bound the accepted score, generated into the validation rule with
`fmt.Sprintf` so the scale is stated once. All of it assumes 5 questions scored 1–4 (total
5–20) — the dashboard quiz now exists and uses exactly that scale, so changing the quiz
means changing these constants.

- `GET /api/risk/profile` (protected) returns the caller's newest result as
  `{"has_profile": bool, "profile": {id, skor, tipe_risiko, created_at} | null}`.
  `has_profile` exists so the frontend can tell "never took the quiz" apart from "the result
  is Moderat", which a bare type string cannot express.
- `POST /api/risk/quiz` rejects any score outside 5–20 with a 422.
- `latestRiskProfileRecord` is the shared row reader; `latestRiskProfile` (used by the chat
  controller for the persona) is a thin wrapper over it.

## Portfolio

`portfolios` holds one row per holding: `id, user_id, asset_symbol, asset_type, quantity,
buy_price, timestamps`, with a cascading FK to `users`. All three routes are behind
`AuthMiddleware` and scoped by `user_id`:

| Route | Behaviour |
| --- | --- |
| `GET /api/portfolio` | `{"data": [...]}` — always an array, never `null` |
| `POST /api/portfolio` | body `{asset_symbol, quantity, buy_price}` → 201 |
| `DELETE /api/portfolio/{id}` | 200, or 404 when it is not the caller's row |

- Only symbols whose live price already exists are accepted (`models.AssetTypeForSymbol`).
  The map covers the ten coins (`BTC`…`LTC`), the nine IDX and seven US stocks (`BBCA`…`META`),
  the metal/mining and food commodities (`XAU`, `XAG`, `HG`…`CPO`), plus legacy `USD`;
  `asset_type` is **derived server-side** from the symbol, so a client cannot store a `BTC`
  row typed as `fiat`. An unknown symbol is a 422.
- The `in:` validation rule is not in this repo's usable set, hence the map lookup rather
  than a rule. `quantity`/`buy_price` use `required|numeric` plus an explicit `> 0` check,
  because fractions (0.5 BTC) are legitimate and no rule expresses "greater than zero".
- `Delete(&models.Portfolio{})` is always called with both `id` and `user_id` in the `Where`;
  `RowsAffected == 0` becomes a 404 so another user's id cannot even be probed.
- Prices are deliberately *not* computed server-side: the frontend uses `/api/market` for
  `XAU`, `/api/crypto`, `/api/saham`, and `/api/komoditas` for the other rows, so there is
  only one price source per asset class.
- `portfolios.user_id` is exposed in JSON (same as `risk_profiles`); the response only ever
  contains the caller's own rows.

## Frontend

One view, `resources/views/dashboard.html`, served at `/` by `DashboardController`, which
passes only `appName` and `year`. Dark futuristic Tailwind theme (with an optional light mode from
the topbar) pulled from `cdn.tailwindcss.com` and `fonts.bunny.net` — **without internet the page
renders unstyled, not blank**, so a live demo needs connectivity or vendored CSS.

The frontend authenticates normally: the token lives in `localStorage` under `fingo_token`
(`fingo_user` for the profile, `fingo_profile` for the last known risk type) and is sent as
`Authorization: Bearer`; a 401 clears the session and the market cards fall back to a "Harga
terkunci" state. No endpoint is public for testing purposes.

- **The topbar has a language menu and a theme toggle.** `#lang-menu` holds `#btn-lang` and
  `#lang-dropdown` with two `.lang-option[data-lang]` entries (ID/EN); the choice lives
  in `localStorage.finanika_lang`, and `applyLanguage()` sets `<html lang>`. Two layers do the
  translating: `data-i18n` attributes with the hand-written `I18N`
  dictionary for sidebar labels, page titles, auth buttons, the welcome line and the chat panel,
  and the generated `public/i18n-content.js` (`window.TEXT_I18N`) for everything rendered by the
  panels. `localizeDom()` walks text nodes and `placeholder`/`title`/`aria-label`, keeps each
  node's Indonesian source in `__i18nSrc`, and a `MutationObserver` re-runs it after every
  render, so switching language updates the current page without a reload and later API rows
  follow too. `TEXT_I18N_EXTRA` in the view overrides/augments the generated dictionary for
  strings the extractor cannot see (profile notes, category names, empty/error states),
  `TEXT_I18N_DYNAMIC` covers JS-built messages and the long source notes, and `PATTERN_I18N`
  handles template strings such as `Tren {name}`, `sebelumnya {value}`, `{n} aset` and
  `{n} hari terakhir`; `RANGE_I18N` relabels the chart pills while `data-range` keeps the
  logic. Chart text built outside the DOM walker is translated at render time: the Aset Dunia
  donut takes its labels/total/tooltip from `translateName`/`translateTextValue`, uses the
  `percentTotal` pattern, and `applyLanguage()` rebuilds the donut when that panel is visible.
  Commodity rows put the unit and separator in one text node (`per troy ounce ·`), so the unit
  keys in `TEXT_I18N_DYNAMIC` carry the trailing `·` as well. A sentence split by an inline `<span>` cannot be matched text-node by text-node, so
  those paragraphs carry `data-i18n-block`: `localizeBlocks()` translates the whole
  `textContent` and stores the original HTML to restore on Indonesian. Nav spans and page `<h1>`s get their key from `NAV_I18N`/`PAGE_I18N` in
  `markTranslatableNodes()` instead of carrying an attribute in the markup; the Dasbor h1 is
  skipped because it already has its own `page.dasbor` span. `t()` falls back to Indonesian,
  then to the key. **AI text is never passed through the dictionary**: `#ai-summary`, the
  insight answers and every chat bubble carry `data-no-i18n`, and the summary placeholder is
  localized explicitly before it is inserted.
  The same `state.language` is sent as `context.language` in the chat request, as
  `?language=` on `GET /api/ai/summary`, and in the `POST /api/insight/explain` body. The
  server adds a "Bahasa jawaban" line to the prompt and localizes the closing label
  (`Suggestion:`); `MarketSummary` and `ExplainNews` take the
  language too, and `aiSummaryCacheKey` includes it so a cached narration is not replayed in
  the wrong language.
- **The theme toggle (`#btn-theme`) switches `html[data-theme]` and persists in
  `localStorage.finanika_theme`.** The markup starts at `data-theme="dark"`, and the early
  `<head>` script now sets the attribute explicitly: stored `light` wins, anything else
  (including an empty value or an unknown string) means dark. That keeps the first visit dark
  and prevents a white flash before the body paints; the script also applies the saved
  language/direction before paint. Light mode is a CSS override block keyed on
  `html[data-theme="light"]` (white surfaces, dark text, `border-gray-300`/`border-gray-400`
  card borders, `shadow-sm`/`shadow-md` elevation, darker input borders and a focus ring,
  `!important` on the Tailwind colour utilities) rather than a second markup; `.glass`,
  `.glass-strong`, `#chat-panel`, `#pasar-modal > div`, the sidebar and the topbar all get the
  stronger treatment so panels stay separated on the pale background. Charts use `chartPalette()` at creation
  (`drawChart`, `renderWorldPie`, `drawPortfolioChart`) and `refreshChartsForTheme()` calls
  `updateOptions` on every entry in `chartRegistry` when the theme changes, so axes, grid lines,
  legend text, tooltip theme and donut labels/strokes follow. `state.theme` mirrors the attribute.
- **The dropdown closes on an outside click and on Escape** (`closeLangMenu()` is also called
  from the global keydown handler), and `#btn-lang` keeps `aria-expanded` in sync. `#lang-label`
  shows the active code. The dropdown is `absolute right-0` inside `#lang-menu`; keep it in that
  ancestor or it will position against the header.
- The Crypto, Kurs, Saham, and Komoditas panels are lists/tables fed by `/api/crypto`,
  `/api/forex`, `/api/saham`, and `/api/komoditas` (no dummy data anywhere), loaded
  on first visit via `showPage` and after login via `loadAccountData()`. The Saham list is split
  client-side on `market` (`SAHAM_TABLES`:
  `saham-id-tbody` / `saham-us-tbody`) into two panels that the **tab buttons** in
  `#saham-tabs` show one at a time. `stockPrice()` in the
  frontend formats each stock row by kind — indices as plain points, `IDR` rows with `idr0`, the
  rest through `usdValue` — and `PAGE_ASSETS` needs an entry per new asset or the chart falls
  back to the coin formatter (8 decimals, USD) and mislabels rupiah prices.
- **Crypto, Saham, and Komoditas share one page shape**: a compact list on the left (icon, name,
  price, change — the equity rows also carry the previous close under the symbol), the chart in
  the wider right column, and the education card full width underneath. The list is still a
  `<tbody>` with the same `data-coin` / `data-asset` rows, so the chart machinery below is
  unchanged; only the row markup got narrower (two cells instead of six, hence `truncate` +
  `min-w-0`). The old BTC hero card that sat at the top-left of the Crypto page is **gone** —
  it was hardcoded to "Bitcoin" and never followed the selected coin, which is what made the
  page look broken when clicking another coin. Do not reintroduce a static label there: the
  selected asset's name belongs to the chart header and the "Tentang" card, both driven by
  `state.cryptoAsset`.
  Komoditas joined that shape later: `komoditasRow` emits the same two cells, the list scrolls
  in a `max-h-[440px]` box beside a `h-[440px]` chart, and the education card below it is the
  full-width one with the enlarged text (`komoditas-edu-content` `text-base leading-loose`, 16/32
  px). There is no gold card on that page: Emas is just the first row.
  - The Komoditas education card is the only one with a **"Baca Selengkapnya"** button
    (`komoditas-edu-link`). It is not a per-asset URL table: `renderEducationPanel` builds
    `https://finance.yahoo.com/quote/<row.symbol>` from whatever row is selected, and an element
    that a panel does not declare (`EDU_PANELS[id].link`) is simply left alone — which is why the
    Kurs panel has no button. Visibility is toggled with an inline `style.display`, not the
    `hidden` class, so the initial markup can start hidden without fighting Tailwind's order.
- **The Kurs page is driven by one selected currency.** `state.forexAsset` feeds four things at
  once — the big card (`paintForexCard`), the chart header, the education panel, and the
  converter — so a row click or the **← Sebelumnya / Selanjutnya →** buttons (`moveForex`, wraps
  around the ten rows) move the whole page. `paintForexCard` is called from `refreshChart` and
  `selectChartRow` for `forex-chart`, like the crypto special case; it also owns the nav buttons'
  disabled state when the list has fewer than two rows. The old static USD/IDR card and its
  `kurs-rate` / `kurs-change` / `kurs-prev` ids are **gone** — `renderMarket` no longer touches
  them, since the card must follow `/api/forex`, not the `/api/market` snapshot.
  The page reads top to bottom as: description → **education panel (full width, first thing under
  the description)** → big card + converter side by side → list left / chart right → nothing.
  The education card belongs *above* the layout it describes, so its rows point down
  ("Klik salah satu baris daftar di bawah …"); `renderEducationPanel`'s own fallback text stays
  direction-neutral because Saham and Komoditas put their lists elsewhere.
  - The list is a real three-column table — `Mata uang` (icon, name, code underneath),
    `Kurs (IDR)` (rate with `sebelumnya …` on a second line) and `24 jam` (`changeCell`) — not the
    five-column one it used to be: half a 1440 px grid is ~528 px, and `min-w-[38rem]` rows forced
    a horizontal scrollbar. The previous close is still exported by `downloadKursCsv` (it reads
    `state.forex.data`, not the DOM) and still frames the hero card; only the table cell moved into
    the sub-line. Rows come from `forexRow`, which also draws the coin-style icon from
    `EDU_CONTENT[asset].icon`, and the box scrolls at `max-h-[440px]` beside a `h-[440px]` chart —
    the same pairing as Komoditas.
- **The converter follows the selected currency and swaps by reordering the DOM.**
  `convRow()` prefers the selected forex row and falls back to `state.market.usd` (so it still
  works on the Dasbor before `/api/forex` arrives), `convRate()` reads `rate` or `idr_rate`, and
  `convCode()`/`conv-fx-label` are repainted by `paintConverter()`. The Tukar button flips
  `state.convSwap`, and `paintConverter` re-appends `#conv-field-idr` / `#conv-field-fx` with
  `appendChild` — the inputs keep their values and listeners, only their order changes.
  `state.convSource` (`'idr'` / `'fx'`) records the last side the user typed in, so `syncConverter`
  recomputes the other side after a rate refresh instead of overwriting the user's number; both
  inputs are guarded by `convLock`. The foreign input is `conv-fx`, not `conv-usd` — it is no
  longer always the dollar.
- **The account pages are `data-panel="profil"` and the Dasbor risk banner.** The topbar
  avatar/name (`#btn-profile`) and the sidebar account block (`#btn-side-profile`) open the
  profile page; the sidebar `Profil Saya` entry is `.js-auth-only` and `showPage('profil')`
  sends guests to the login modal. `loadProfileForm()` paints the cached session first, then
  refreshes it from `GET /api/me`; the email input is `readOnly`, the bio textarea is
  `maxlength=100`, and the gender select is the only other field. `submitProfile()` sends
  `FormData` (with the avatar when one is selected) to `PUT /api/profile`; the preview is a
  local object URL that `clearProfileAvatarPreview()` revokes. Saving repaints `paintAvatar()`
  in both the topbar and sidebar, so the two never disagree. Logout restores the empty form
  and leaves the page for Dasbor.
- **`#risk-onboarding` is shown only for a signed-in non-admin without a risk profile.**
  `loadRiskProfile()` returns the payload from `GET /api/risk/profile`; `paintRiskNotice()`
  hides the banner when `has_profile` is true, when the user is an admin, or when logged out.
  Login and registration await `loadAccountData()` and open `kuis` when the profile is still
  missing, while an existing session simply shows the banner on Dasbor. Submitting the risk
  quiz sets `state.hasRiskProfile = true`, so the banner disappears immediately.
- **Komoditas has two tabs like Saham.** `state.komoditasTab` (`'logam'` default) is applied
  by `paintKomoditasTabs()`, which toggles `.tab-btn.active` and hides the non-matching rows
  from `renderKomoditas`; `setKomoditasTab()` calls `ensureKomoditasTabSelection()` so a
  selected asset that moved to the other group falls back to the first visible row.
  `#komoditas-tabs` is delegated, `data-komoditas-tab="logam"` holds metals/mining and
  `"pangan"` holds food/agriculture (including the four PIHPS rows). The chart, education card,
  and source count all follow the visible tab; adding a tab means one button, one group in
  `komoditasGroup()`, and the row filter.
- **A row chart may be fed by more than one table.** `ROW_CHARTS[id].tbody` accepts a single id
  or an array of ids, and `chartBodies(chart)` normalises it; `paintChartSelection` and the
  click delegation both go through it, which is what lets the two Saham lists share one chart
  (the hidden tab's rows are painted too — harmless, they are not on screen).
- **Saham tabs** are state, not just CSS: `state.sahamTab` (`'id'` default) is applied by
  `paintSahamTabs()` (toggles `.tab-btn.active` and `.hidden` on every `[data-market-panel]`),
  and `setSahamTab()` also calls `ensureSahamTabSelection()`, which moves `state.sahamAsset` to
  the first row of the newly opened market when the current pick belongs to the other one. That
  helper is called from `renderSaham` too, so opening the page never leaves a foreign chart
  under the Indonesia tab. Adding a market means one `SAHAM_TABLES` entry, one `data-market-panel`
  block (with its `<key>-tbody` / `<key>-empty`) and one tab button.
- The **Aset Dunia** page (`data-panel="aset-dunia"`, sidebar entry right after Belajar) is
  served by `GET /api/world-assets` (auth) since Fase 14 — the numbers used to be a ten-row
  `WORLD_ASSETS` literal in the view. The response is
  `{data: [{name, ticker, icon, category, color, value, basis, live}], live, fetched_at}`, already
  sorted by `value` descending, and `WORLD_BADGES` colours the category pill. There is no static
  list any more: `state.world` holds the payload and `renderWorldAssets()` fills the four-column
  `#world-tbody` (the per-row `basis` lives in the row's `title` and in the pie tooltip).
  Twenty rows is a curation, not literally the twenty largest — the bases differ per category
  (physical stock, reserves, money supply, commercial property, government and corporate debt,
  insurance assets), so the footnote still says the order is not an official ranking; Properti's
  figure is the *investable* reading, not the ~$600 T "all land and housing" number, which would
  swamp the pie.
- **`WorldAssetService` (`app/services/world_asset_service.go`) owns the table**, not the view:
  `worldAssetSeeds` holds the twenty rows and, on four of them, `LiveKey` + `LiveUnits` — the
  physical quantity behind the estimate. `worldAssetRows()` multiplies that quantity by the live
  price and marks the row `live: true`, so Emas (216.000 ton), Perak (1,74 juta ton), Bitcoin
  (19,9 juta BTC) and Ethereum (120 juta ETH) move with the market while the rest keep their
  estimate. `sort.SliceStable` runs **after** that, because a live row can change places — do not
  move the ordering back into the literal. The whole list is cached 15 minutes under
  `market_world_assets`, and the four live keys are the asset names the other tables already use
  (`gold` / `bitcoin` from the market payload, `silver` from the commodity table, `ethereum` from
  the coin table).
- **The live prices are taken from the paths that already exist**, which is what keeps Emas and
  Perak consistent with the other pages: `GetCurrentPrices()` for gold (xaus.com) and Bitcoin
  (CoinGecko), and `fetchYahooQuotes()` for silver and Ethereum — with the symbols read out of the
  commodity and coin tables (`historyYahooSymbol("silver")`, `cryptoCoinByID("ethereum")`) rather
  than retyped here. A failed price is not fatal: that row falls back to its estimate and keeps
  `live: false`, which is why this endpoint never answers 502. Only `live` in the response tells
  the page how many rows are real market numbers, and `#world-live-note` spells them out
  ("4 dari 20 angka mengikuti harga pasar terkini (Emas, Perak, Bitcoin, Ethereum)").
- **`#world-pie` is a donut over the same `state.world`, and the topbar toggle drives it.**
  `worldValue()` is the single formatter for the table cells, the donut's centre label and the
  tooltip: `$29,0 triliun` in USD mode, `Rp 464,0 kuadriliun` in IDR mode (triliun dolar ×
  `liveRate()` ÷ 1000 — 1 kuadriliun = 1.000 triliun, so no rows of zeros). `renderWorldPie()`
  destroys and rebuilds the instance on `setCurrency` **only while the panel is visible**
  (`panelVisible('aset-dunia')`) and bails out when the container has no width, because
  ApexCharts needs a laid-out container; it is drawn from `showPage('aset-dunia')` through
  `loadWorldAssets().then(…)`, never at boot. The centre label is computed at render time, which
  is why a rebuild is needed at all — the tooltip's `custom` callback would follow the toggle by
  itself. Twenty slices need two things the ten-row version did not: the data-label formatter
  hides slices under 3%, and the legend is 10px with tighter item margins.
- `loadWorldAssets()` follows the other loaders (`loadCrypto`, …): called from `loadAccountData()`
  and from `showPage`, it renders straight from `state.world` when it is already there, so
  toggling the currency or coming back to the page does not re-fetch. `worldEmptyMessage()`
  distinguishes the three empty states (no token / still loading / genuinely empty) and
  `resetWorldAssets()` restores the logged-out shape on logout. That message goes through
  `textContent`, not `innerHTML`, because it can carry an API error message.
- **The CSV buttons are fed from state, never from a fresh request.** `downloadCryptoCsv` /
  `downloadKursCsv` read `state.crypto.data` / `state.forex.data` — what the table is showing —
  and write it through `downloadCsv`, which builds a `Blob`, clicks a temporary anchor and
  revokes the object URL on a 1 s timer (revoking immediately cancels the download in some
  browsers). `csvCell` quotes per RFC 4180 and `csvNumber` exports raw numbers with a dot
  decimal separator, so the file opens cleanly in Excel *and* pandas; do not export the
  formatted strings (`Rp 1.470.852.000`). An empty table logs a status message instead of
  downloading a header-only file. The Crypto button now sits in the `Daftar Koin` card header,
  not in a `<thead>` — the coin list has no header row any more, so look there for it.
- **The Crypto chart follows the coin list**: clicking a row calls `selectChartRow('crypto-chart', …)`,
  which sets `state.cryptoAsset`, retitles the card ("Tren Ethereum"), marks the row
  (`.coin-row.active`, defined next to `.chart-row.active` in the view's CSS), updates the
  "Tentang" card through `renderCoinAbout(asset)` and reloads `/api/market/history` for that coin
  id. Listeners are delegated off `#crypto-tbody` because the rows are redrawn on every load.
  `state.cryptoAsset` starts at `bitcoin` and resets on logout; the header reads `state.crypto`
  (written by `loadCrypto` *before* `renderCrypto`, like the other panels).
- `ASSET_API` only maps the dashboard chart's symbols (BTC/XAU/USD); a coin id is its own
  `asset` value, hence `ASSET_API[symbol] || symbol`. Line colour and price format come from
  `chartStyle` — `ASSETS` for the dashboard chart, the per-coin `COIN_COLORS` map otherwise —
  and sub-$1 coins stay readable because the formatter is `usdPrice`, not `usd0`.
- Index points have no currency, so `renderSaham` formats them with `indexPoints` (Indonesian
  thousands separator, no symbol); commodities are USD and go through `usdValue`, which falls
  back to a dash instead of `$0.00` when a quote is missing.
- The Portfolio panel is server-backed too (`/api/portfolio`): add/delete round-trip to the
  API and the list is re-fetched afterwards. **`fingo_portfolio` is gone** — do not
  reintroduce a localStorage copy, or the panel will disagree with the database.
- **Portfolio and Kalkulator DCA share `ASSET_CATALOG`** in the view: Crypto, Saham
  (Indonesia/Luar Negeri), and Komoditas (Logam & Tambang/Bahan Pangan). `syncAssetPicker()`
  fills `#pf-*` and `#dca-*` from the same entries, and `unitPriceIdr()` reads the already
  loaded `state.crypto` / `state.saham` / `state.komoditas` / `state.market`. A new asset must
  be added both there and to `portfolioAssetTypes` in `app/models/portfolio.go` if it should
  be storable. `refreshPortfolioPrices()` revalues holdings when a list arrives later, and
  `prefillDcaPrice()` only clears the field when the chosen asset has no live price yet.
- The risk badge is synced from `GET /api/risk/profile` on login (`loadRiskProfile`), so the
  profile survives a different browser instead of resetting to the `localStorage` value.
- `renderCrypto` / `renderForex` / `renderSaham` / `renderKomoditas` / `renderPortfolio` decide
  the empty-row message from state (not logged in vs. failed vs. genuinely empty); keep that
  distinction when editing them. The message is keyed by table prefix — `komo-tbody` /
  `komo-empty` / `komo-fetched` — so the commodity table's prefix is `komo`, not `komoditas`.
- `changeCell(value)` is a module-level helper shared by the pasar, crypto, kurs, saham, and
  komoditas tables.
- **Insight Global** is the news page (`data-panel="insight"`, third nav entry under Menu,
  right after Belajar). Its cards come from `/api/insight` (`data-panel` grid is
  `md:grid-cols-2`); each card's "Tanya FinGo AI" button posts `{id, title}` to
  `/api/insight/explain` and writes the answer into `#insight-answer-<id>` inside that same
  card. The list is re-rendered on every visit, so the button is delegated off
  `#insight-list`. `#insight-source` states where the list came from ("Google News · N berita"
  vs "Daftar contoh · N berita") straight from the API's `source`, and `insightBadge()` picks
  the badge colour per category (`INSIGHT_BADGES`, unknown categories fall back to cyan).
- **The Dasbor has exactly one AI card, and it reads a cached endpoint** (Fase 10/11). The
  "FINANIKA Financial Advisor" card (`#ai-summary`) is back, driven by `askSummary(silent)` →
  `GET /api/ai/summary`, both from the boot auto-load (`setTimeout(…, 400)` once a token exists)
  and from the "Minta ringkasan" button. It deliberately does **not** go through
  `/api/chat/send` any more: that path wrote a fake user message plus the answer into
  `chat_messages` on every page load and bypassed the 30-minute cache entirely, which is what
  burned the Gemini quota. The per-asset AI Analysis card (`#market-analysis`, `analyseAsset`,
  `syncMarketAnalysis`, `refreshAnalysis`, `paintAnalysisButton`, the `ANALYSIS_*` maps and the
  cooldown/cache state) stays **removed**. `fillParagraphs()` and `analysisFailure()` survive
  because this card and the Insight Global cards use them — `fillParagraphs()` pipes every text
  through `cleanMarkdown()`, and `addBubble()` does the same for **assistant** bubbles only
  (the user's own message is shown verbatim).
  `paintSummaryMeta()` prints where the narration came from ("Baru dibuat AI" vs "Dari cache
  AI · disusun HH.MM · berlaku 30 menit") — deliberately without naming a provider, because the
  answer may have come from Groq (the card pill and the chat header say "Gemini + Groq");
  `paintSummaryPrices()` prints the price line above
  it and is also called from `renderMarket` so it follows the USD/IDR toggle, and
  `resetSummary()` (logout *and* boot) restores the placeholder because the narration belongs to
  the user's risk profile. `SUMMARY_PLACEHOLDER` in the JS must keep matching the copy in
  `#ai-summary`'s markup. Do not add an auto-submit or a second self-loading AI card here.
- **The Crypto page has no AI Analysis card either** (Fase 4): it was replaced by **"Tentang
  <koin>"**, fed by the static `COIN_ABOUT` map (per-coin description + official whitepaper
  link, keyed by CoinGecko id) through `renderCoinAbout(coinId)` / `resetCoinAbout()`. Clicking
  a coin row updates it from `selectChartRow`/`refreshChart`, so it costs no Gemini quota — the
  whole reason it replaced the AI card, which kept hitting the rate limit. `COIN_ABOUT` also
  covers `avalanche-2` and `chainlink`,
  which are not in today's `cryptoCoins`, so swapping the coin list cannot leave a blank card;
  an unknown id hides the whitepaper button and says so.
- **The four list|chart pages share one alignment rule** (Fase 9): the grid is
  `items-stretch` and both cards are `h-full`, so the list card and the chart card always end
  on the same pixel — that is the whole point of the rule, do not go back to `items-start`
  (`deltaBottom` must stay 0 for Kurs, Saham, Komoditas, and Crypto). The list card is a
  `flex flex-col`, its scroll box `min-h-0 flex-1`, and the chart div `min-h-[440px] flex-1`
  with the card as a flex column, so neither side is left with a hollow strip. The old
  `max-h-[440px]` on the list is **gone**: the list now drives the height and the chart fills
  whatever is left, which is why the ten rows are visible without an inner scrollbar. The two
  Saham tab panels are flex columns too, and the view's own CSS carries
  `[data-market-panel].hidden { display: none }` so `.hidden` keeps beating Tailwind's `.flex`.
- **Semua Pasar (`data-panel="pasar"`, Fase 9) is an aggregator, not a page with its own
  endpoint.** It calls the four existing loaders (`loadCrypto`/`loadForex`/`loadSaham`/
  `loadKomoditas`) in `Promise.all`, then builds one flat list from `state.*` — `PASAR_GROUPS`
  is the order of the groups *and* of the rows, and `state.pasarCategory` maps asset → group so
  the row objects stay untouched (that is what keeps `rowPrice`/`rowLabel`/`rowAsset` working on
  them). 60 rows: 10 koin, 20 saham, 20 komoditas, 10 kurs — a group whose endpoint failed just
  does not appear. `renderPasar` draws group header rows plus the rows, and the category pills
  (`#pasar-filters`) and the search box filter in place; nothing re-fetches. `renderMarket` no
  longer feeds this table — `/api/market` only drives the three Dasbor cards now, and
  `pasar-fetched` is stamped by `paintPasarStamp()` from the newest of the four `fetched_at`.
- **"Detail Aset" opens `#pasar-modal`**, a chart + education pop-up driven by a fifth
  `ROW_CHARTS` entry (`pasar-detail-chart`) so the range pills, the history cache and the price
  formatters are reused rather than duplicated. Two details matter: the entry is flagged
  `manual: true` so the boot loop does *not* attach the row-click delegation to `pasar-tbody`
  (the page's own listener opens the modal instead), and its `chartParams` days come from
  `CHART_RANGES['pasar-detail-chart'] = 'pasarDetailRange'`. There is deliberately **no**
  `EDU_PANELS` entry — `paintPasarDetail()` fills the header *and* the education text, because
  coins are explained by `COIN_ABOUT` while everything else comes from `EDU_CONTENT`. The modal
  must be opened *before* `refreshChart` runs: `loadChartData` bails on a zero-width container.
- **Only the button opens that modal** (Fase 10). The `#pasar-tbody` listener keeps using
  `event.target.closest('[data-detail]')` but must return early when there is none — a click on
  the row, its name, price, 24-hour badge or source cell does nothing. The row keeps the
  `.chart-row` class only so `paintChartSelection` can mark the asset that the modal is showing,
  which is why the view's CSS pins `#pasar-tbody .chart-row { cursor: default; }` and the row
  carries no click-looking `title`/hover; the `title="Buka detail …"` sits on the button.
  Do not put an `openAssetDetail` call back on the `<tr>`: the whole page is a tall table, so a
  row-wide hit area fires while the user is only scrolling or selecting text.
- **`askFinGo(prompt)` is the one path into the chat**, used by the modal's "Tanya FinGo AI"
  button (which asks `Berikan analisis singkat dan saran DCA untuk aset <nama> saat ini.`) and by
  the `[data-prompt]` chips. It opens the panel, fills `#chat-input` and dispatches a submit, so
  the guest/no-token path and the 429/503 amber bubble behave exactly as they do for typed
  questions. `pasarName(row)` prefers `name (pair)` for kurs so the question names USD/IDR rather
  than "Dolar Amerika Serikat".
- **The chat form carries a mic and a clip button.** `#btn-mic` starts `SpeechRecognition` /
  `webkitSpeechRecognition` (`lang = 'id-ID'`, `interimResults = false`, `continuous = false`)
  and writes the final transcript into `#chat-input` after whatever the user had already typed
  (`chatMicBase`). While recording the button gets `.recording` — red text, red border and the
  `micPulse` ring — which is the only place red is used on the panel; `state.chatRecording` is the
  source of truth, and `stopVoiceInput()` detaches the handlers *before* calling `stop()` so
  `onend` cannot re-enter. Construction and `start()` are wrapped in `try/catch` because Edge on
  `localhost` used to fail silently, and `voiceLog()` prints `[FINANIKA][mic]` lines for
  `onstart`/`onaudiostart`/`onspeechend`/`onerror`/`onnomatch`/`onend` so a failed run names the
  step. Every error code is mapped to a sentence by `voiceErrorMessage` (`not-allowed`,
  `service-not-allowed`, `no-speech`, `audio-capture`, `network`, `language-not-supported`, other
  codes with their code appended). Closing the panel, logging out and submitting all stop the
  recording, because a live microphone behind a hidden panel is a privacy bug. Browsers without
  the API get one amber bubble ("Browser Anda tidak mendukung fitur suara, atau izin mikrofon
  ditolak.") instead of a dead button.
- **The clip button is an image attachment, not an upload field.** `#btn-chat-image` opens the
  hidden `#chat-image-input`; `prepareChatImage` redraws the file on a canvas capped at
  `CHAT_IMAGE_MAX_EDGE` (1280 px) and exports JPEG 0.85, so a 4 MB phone screenshot leaves as a
  few hundred KB. The result lives in `state.chatImage` as `{blob, name, url}` and is previewed in
  `#chat-image-preview` (thumbnail + filename + remove button) *above* the input, never inside it.
  `sendChat` posts `FormData` when `state.chatImage` is set and plain JSON otherwise — both hit
  `/api/chat/send`, and `api()` grew an `options.form` branch that deliberately does not set
  `Content-Type` (the browser must add the multipart boundary). The user bubble shows the picture
  via `addImageBubble`, which revokes the object URL once the `<img>` loads; `clearChatImage`
  revokes eagerly everywhere else. An image with no typed text is allowed and gets a default
  analysis question on the server, so the Kirim button is not a dead end.
  `loadAccountData()` also calls `loadInsight()` now: the five headlines the chat sends come from
  `state.insight`, and the server list is cached for an hour, so this is one request per session.

- **Rekomendasi Platform (`data-panel="platform"`, the first nav entry under Asisten) is
  DB-backed since the admin CRUD work.** The thirty-six seed rows live in
  `investment_platforms` (15 crypto, 16 saham, 5 Komoditas/Emas; 22 lokal, 14 luar negeri)
  and arrive through the public `GET /api/platforms` into `state.platforms`. Every row
  carries `category` (`crypto`/`saham`/`komoditas`), `type` (`Lokal`/`Luar Negeri`),
  `logo_domain`, a description, and `pros`/`cons` arrays, and `renderPlatforms()` is the
  single renderer. **Each entry must carry at least three `pros` and three `cons`, and at
  least one of each must talk about cost** — fee, biaya, spread, komisi, or admin — because
  price and safety are what the page exists to explain; `cleanAdminPlatform` enforces it.
  Two independent pill groups filter it *in place* — `#platform-filters` on category and
  `#platform-origins` on origin — both through `setPillGroup` + `renderPlatforms()`, so a
  filter click re-draws the grid and re-stamps `#platform-count` without touching the
  network. `#platform-empty` is toggled by whether the filtered list came out empty.
  `loadPlatforms()` is called at boot and from `showPage('platform')`; it re-renders from
  state once loaded, and retries after an error instead of pinning the failure.
  - **Logos are fetched, not bundled**: `platformLogoURL()` asks
    `https://www.google.com/s2/favicons?domain=<logo_domain>&sz=128`, and the `<img>` is
    positioned *absolute* over a tile that already holds the platform's first letter. That old
    endpoint answers 404 for some legitimate domains (`bibit.id` is one), so the image carries
    `data-fallback-src` pointing at Google's newer
    `https://t2.gstatic.com/faviconV2?...&url=https://<logo_domain>&size=128`; the error
    handler tries that once and only then removes the image, leaving the letter visible
    instead of an empty box. That is why the tile has two layers and why `renderPlatforms`
    re-attaches the handler after every `innerHTML` write. The seed picks real domains the
    favicon service can serve — `bnisekuritas.co.id`, `sinarmassekuritas.co.id`,
    `sekuritas.miraeasset.co.id` and `itiger.com` — not prettier guesses.
  - `renderPlatforms()` runs at boot and from `showPage('platform')`, but `logout()` does not
    reset `state.platformCategory`/`state.platformOrigin` on purpose: the page is public and
    holds no user data, unlike the panels that fall back to a "login dulu" message. Adding a
    platform is done from Panel Developer's **Kelola Platform** tab; the count, the filters'
    denominators and the empty state all follow from `state.platforms`. `#platform-grid` has
    no `items-start`, so every card in a row stretches to the tallest one and the
    "Kelebihan"/"Kekurangan" blocks line up across the row.

`public/` is served via `Route().Static("public", "./public")` and is currently empty.

## Testing & data

`tests/feature/example_test.go` is the only feature test and it is trivial. `tests.TestCase`
calls `bootstrap.Boot()` in `init()`, so running feature tests requires a reachable database and
a valid `.env` - they are not hermetic. Use the Goravel pattern (testify `suite` +
`tests.TestCase`) when adding feature tests. `app/services/ai_context_test.go` holds pure unit
tests for the AI context caps (`formatNewsDigest` keeps five headlines, platform descriptions are
included and capped); run them with `go test ./app/services`.

Database is MySQL (`DB_CONNECTION=mysql`, driver `goravel/mysql`), database `db_edukasi_go`,
intended to run alongside XAMPP. Local CLI: `C:\xampp\mysql\bin\mysql.exe -u root`.
Migrations are listed explicitly in `bootstrap/migrations.go`.
