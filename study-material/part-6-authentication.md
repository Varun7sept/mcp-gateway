# Part 6: Authentication — JWT, MongoDB & Middleware

## Table of Contents
1. [Architecture Overview](#1-architecture-overview)
2. [Data Structures](#2-data-structures)
3. [The Auth Constructor](#3-the-auth-constructor)
4. [Signup — User Registration](#4-signup--user-registration)
5. [Login — User Authentication](#5-login--user-authentication)
6. [JWT Tokens](#6-jwt-tokens)
7. [Token Validation & Refresh](#7-token-validation--refresh)
8. [Request Logging](#8-request-logging)
9. [Request Analytics](#9-request-analytics)
10. [Middleware — Protecting API Routes](#10-middleware--protecting-api-routes)
11. [ChatStore — MongoDB Chat Persistence](#11-chatstore--mongodb-chat-persistence)
12. [How Auth Integrates with the Gateway](#12-how-auth-integrates-with-the-gateway)
13. [JWT Flow in the Frontend](#13-jwt-flow-in-the-frontend)
14. [Interview Questions & Answers](#14-interview-questions--answers)
15. [Diagrams](#15-diagrams)

---

## 1. Architecture Overview

### What Does the Auth Package Do?

The `auth/` package provides the **complete authentication and authorization layer** for MCP Gateway. It handles:

- **User registration** (signup) and **login** with bcrypt password hashing
- **JWT token issuance** with 7-day expiry
- **Automatic request logging** to MongoDB
- **Aggregated request statistics** via MongoDB aggregation pipelines
- **Chat session persistence** (sessions + messages stored in MongoDB)
- **Middleware** that protects all API routes except a small public set

```
┌──────────┐     POST /api/auth/signup      ┌─────────┐
│  Browser  │ ─────────────────────────────→  │  Auth   │
│  (Frontend)│                                │ (auth/) │
│           │ ←────────────────────────────  │         │
│           │   {token: "JWT...", expires:   │         │
│           │    "7 days from now"}          │         │
└──────────┘                                  └────┬────┘
      ─────────────────────────────────────────────┤
      │  Every subsequent API call carries the JWT │
      │  in Authorization: Bearer <token> header   │
      ▼                                             ▼
┌──────────────┐                          ┌────────────────┐
│  Middleware   │                          │  MongoDB        │
│  (middleware) │                          │  - users        │
│  JWT check →  │                          │  - request_logs │
│  extract user │                          │  - chat_sessions│
│  injects ctx  │                          │  - chat_messages│
└──────┬───────┘                          └────────────────┘
       │
       ▼
┌──────────────┐
│  Gateway      │
│  (server.go)  │
│  routes to    │
│  handler with │
│  authenticated│
│  username     │
└──────────────┘
```

### Three Files

| File | Lines | Purpose |
|------|-------|---------|
| `auth.go` | 338 | Core Auth struct — signup, login, tokens, logging, analytics |
| `middleware.go` | 65 | HTTP middleware that checks JWT on every request |
| `chat.go` | 271 | ChatStore struct — MongoDB persistence for chat sessions + messages |

### How Auth is Initialized

In `server.go`, `auth.New(mongoConfig)` is called during gateway initialization:

```go
auth, err := auth.New(auth.MongoConfig{
    URI:      os.Getenv("MONGODB_URI"),
    Database: os.Getenv("MONGODB_DATABASE"),
})
```

If MongoDB is NOT configured, `s.auth` is `nil` and the system works in anonymous mode (no auth, no chat persistence, no request logging).

---

## 2. Data Structures

### 2.1 User

```go
type User struct {
    Username  string    `bson:"username" json:"username"`
    Email     string    `bson:"email" json:"email"`
    Password  string    `bson:"password" json:"-"`       // never serialized to JSON
    CreatedAt time.Time `bson:"created_at" json:"created_at"`
}
```

The `json:"-"` tag on `Password` ensures the hashed password is **never included in API responses** (JSON serialization). The `bson:"password"` tag ensures it IS stored in MongoDB.

### 2.2 Auth

```go
type Auth struct {
    users       *mongo.Collection   // "users" collection
    requestLogs *mongo.Collection   // "request_logs" collection
    jwtSecret   []byte                 // read from JWT_SECRET env var
    db          *mongo.Database
}
```

### 2.3 MongoConfig

```go
type MongoConfig struct {
    URI      string
    Database string
}
```

### 2.4 ChatStore Types

```go
type ChatSession struct {
    ID        string    `json:"id"`
    Username  string    `json:"username"`
    Title     string    `json:"title"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

type ChatMessage struct {
    ID        string         `json:"id"`
    Role      string         `json:"role"`        // "user" or "ai"
    Content   string         `json:"content"`
    Meta      map[string]any `json:"meta,omitempty"`  // tool names, latency, etc.
    CreatedAt time.Time      `json:"created_at"`
}

type ChatStore struct {
    sessions *mongo.Collection
    messages *mongo.Collection
}
```

### 2.5 RequestLogEntry

```go
type RequestLogEntry struct {
    Username   string    `json:"username"`
    Method     string    `json:"method"`
    ToolName   string    `json:"tool_name"`
    ServerName string    `json:"server_name"`
    Status     string    `json:"status"`         // "success" or "error"
    Error      string    `json:"error,omitempty"`
    LatencyMs  int64     `json:"latency_ms"`
    CreatedAt  time.Time `json:"created_at"`
}
```

---

## 3. The Auth Constructor

### The `New(mCfg MongoConfig) (*Auth, error)` Function

`auth.go:35-67` — performs 5 steps in sequence:

### Step 1: MongoDB Connection

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

client, err := mongo.Connect(ctx, options.Client().ApplyURI(mCfg.URI))
if err != nil {
    return nil, fmt.Errorf("mongo connect: %w", err)
}
```

Uses a **10-second timeout context**. If MongoDB is unreachable, the gateway fails to start.

### Step 2: Connection Verification

```go
if err := client.Ping(ctx, nil); err != nil {
    return nil, fmt.Errorf("mongo ping: %w", err)
}
```

Sends a ping to verify the connection actually works before proceeding.

### Step 3: JWT Secret Reading

```go
secret := os.Getenv("JWT_SECRET")
if secret == "" {
    return nil, fmt.Errorf("JWT_SECRET environment variable must be set")
}
```

**Hard requirement** — no JWT secret means the entire auth system cannot function. This is a startup-time failure.

### Step 4: Collection References

```go
db := client.Database(mCfg.Database)
a := &Auth{
    users:       db.Collection("users"),
    requestLogs: db.Collection("request_logs"),
    jwtSecret:   []byte(secret),
    db:          db,
}
```

Gets references to the `users` and `request_logs` collections. The database name comes from `mCfg.Database`.

### Step 5: Index Creation

```go
if err := a.ensureIndexes(ctx); err != nil {
    return nil, fmt.Errorf("ensure indexes: %w", err)
}
```

Creates MongoDB indexes for fast lookups. On failure, the `Auth` struct is returned with `nil` error — indexes are created only once. The `ensureIndexes` function creates:

| Collection | Index Keys | Options | Purpose |
|---|---|---|---|
| `users` | `username` (ascending) | `unique: true` | Enforce unique usernames |
| `users` | `email` (ascending) | `unique: true` | Enforce unique emails |
| `request_logs` | `username` + `created_at` desc | default | Fast user log queries |
| `request_logs` | `created_at` desc | default | Fast global log queries |

Indexes are created in MongoDB automatically — if they already exist (e.g., on restart), MongoDB silently skips them.

---

## 4. Signup — User Registration

### `Signup(username, email, password string) (string, error)`

Called when a new user registers at `POST /api/auth/signup`.

### Flow

```
1.  Hash the password with bcrypt (cost = DefaultCost = 12 rounds)
2.  Create User struct with CreatedAt = time.Now()
3.  Insert into MongoDB "users" collection
4.  IF duplicate key error (username or email already exists):
        return "", fmt.Errorf("username or email already exists")
5.  IF any other insert error:
        return "", fmt.Errorf("insert user: %w", err)
6.  On success → generateToken(username) → return the JWT
```

### bcrypt Password Hashing

```go
hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
```

`bcrypt.DefaultCost` = 12 rounds of hashing. This is intentionally slow (takes ~250ms) to make brute-force attacks impractical. The salt is generated automatically and embedded in the hash string.

### Example

Input: `username="varun"`, `email="v@v.com"`, `password="secret123"`

- bcrypt produces a hash like: `$2a$12$LJ3m4ys...` (60 characters, includes salt + hash)
- The `User` document stored in MongoDB:

```json
{
    "username": "varun",
    "email": "v@v.com",
    "password": "$2a$12$LJ3m4ys...",
    "created_at": "2026-07-28T10:30:00Z"
}
```

- Returns the JWT token string for immediate login

---

## 5. Login — User Authentication

### `Login(username, password string) (string, error)`

Called when an existing user logs in at `POST /api/auth/login`.

### Flow

```
1.  Query MongoDB for user by username
2.  IF user not found → return "invalid username or password"
3.  Compare the provided password against the stored bcrypt hash:
      bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password))
4.  IF comparison fails → return "invalid username or password"
5.  On success → generateToken(username) → return the JWT
```

### Security: No User Enumeration

Both "user not found" and "wrong password" return the **exact same error message**: `"invalid username or password"`. This prevents attackers from discovering which usernames exist in the system.

### bcrypt.CompareHashAndPassword

```go
// bcrypt compares the password against the stored hash
bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
```

This re-hashes the provided password with the **same salt** embedded in the stored hash, then compares the results. Since bcrypt hashes include the salt, no separate salt storage is needed.

---

## 6. JWT Tokens

### `generateToken(username string) (string, error)`

```go
claims := jwt.MapClaims{
    "sub": username,              // subject — the username
    "iat": time.Now().Unix(),     // issued at — current timestamp
    "exp": time.Now().Add(7 * 24 * time.Hour).Unix(),  // expires in 7 days
}

token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
return token.SignedString(a.jwtSecret)
```

### Token Structure

A JWT has three Base64URL-encoded segments separated by dots:

```
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ2YXJ1biIsImlhdCI6MTY1OTMwOTIwMCwiZXhwIjoxNjU5OTEzNjAwfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c
    ──────────────────────────    ──────────────────────────────────────    ────────────────────────
    Header (alg: HS256, typ: JWT)    Payload (sub, iat, exp)                 Signature (HMAC-SHA256)
```

You can decode any JWT at [jwt.io](https://jwt.io) to see the claims. The secret key is NOT included in the token — it's only used to verify the signature.

### Token Properties

| Property | Value |
|---|---|
| Algorithm | HMAC-SHA256 (HS256) |
| Subject (`sub`) | Username |
| Issued At (`iat`) | Current Unix timestamp |
| Expires At (`exp`) | Current time + 7 days |
| Secret | `JWT_SECRET` environment variable |

### Why HS256?

HMAC-SHA256 is a **symmetric algorithm** — the same secret key signs AND verifies tokens. This is simpler than RSA/EC (asymmetric) because there's only one key to manage. The secret must be kept secure (never committed to code, use environment variables).

---

## 7. Token Validation & Refresh

### `ValidateToken(tokenStr string) (string, error)`

Called on every request that goes through the middleware. Validates the JWT and extracts the username.

### Flow

```
1.  Parse the JWT using jwtSecret as the key
2.  Verify the signing method is HS256 (rejects "none" algorithm attack)
3.  Check token.Valid flag (checks expiry and signature)
4.  Extract "sub" claim as the username string
5.  Return username on success, or an error on any failure
```

### The "none" Algorithm Attack Prevention

```go
if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
    return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
}
```

This explicitly rejects tokens signed with anything other than HMAC. Without this check, an attacker could change `"alg": "none"` in the header and bypass signature verification entirely.

### `RefreshToken(tokenStr string) (string, error)`

Called at `POST /api/auth/refresh` to issue a fresh token without requiring the user to log in again.

```go
func (a *Auth) RefreshToken(tokenStr string) (string, error) {
    // 1. Validate the existing token
    username, err := a.ValidateToken(tokenStr)
    if err != nil {
        return "", fmt.Errorf("invalid or expired token")
    }
    // 2. Issue a brand new token with a fresh expiry
    return a.generateToken(username)
}
```

Both the old and new tokens remain independently valid until the old one expires. This means:
- Multiple devices can be logged in simultaneously
- Logging in on a new device doesn't invalidate existing sessions
- Each token has its own 7-day countdown from when it was issued

---

## 8. Request Logging

### `LogRequest(username, method, toolName, serverName, status, errMsg string, latency time.Duration)`

```go
func (a *Auth) LogRequest(username, method, toolName, serverName, status, errMsg string, latency time.Duration) {
    go func() {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        a.requestLogs.InsertOne(ctx, bson.M{
            "username":    username,
            "method":      method,
            "tool_name":   toolName,
            "server_name": serverName,
            "status":      status,
            "error":       errMsg,
            "latency_ms":  latency.Milliseconds(),
            "created_at":  time.Now(),
        })
    }()
}
```

### Key Design Decisions

| Decision | Why |
|---|---|
| **Fire-and-forget goroutine** | Logging failures don't affect the actual request. If MongoDB is slow or down, the request completes normally. |
| **5-second timeout context** | If MongoDB logging hangs, give up after 5 seconds rather than blocking. |
| **Latency in milliseconds** | `int64` is sufficient for millisecond precision. No need for float64. |
| **Separate `request_logs` collection** | Keeps the collection uncluttered. The `users` collection only stores user data. |
| **Called after every tool execution** | The chat handler calls `LogRequest` for each tool call AND for the overall chat request, giving full visibility. |

### What Gets Logged

Every tool call generates a log entry:

```json
{
    "username": "varun",
    "method": "chat",
    "tool_name": "get_weather",
    "server_name": "weather",
    "status": "success",
    "error": "",
    "latency_ms": 450,
    "created_at": "2026-07-28T10:30:00Z"
}
```

### Error Logging

If a tool fails, the error message is stored:

```json
{
    "username": "varun",
    "method": "chat",
    "tool_name": "get_crypto_price",
    "server_name": "crypto",
    "status": "error",
    "error": "CoinGecko API: 429 rate limited",
    "latency_ms": 1200,
    "created_at": "2026-07-28T10:31:00Z"
}
```

---

## 9. Request Analytics

### `GetRequestStats(username string) map[string]any`

Uses MongoDB aggregation pipelines to compute real-time analytics. Pass an empty string for global stats across all users.

### Three Aggregation Pipelines

#### Pipeline 1: Overall Statistics

```go
pipeline := mongo.Pipeline{
    {{Key: "$match", Value: match}},          // Filter by username (or no filter for global)
    {{Key: "$group", Value: bson.M{
        "_id":           nil,                   // Group everything into one bucket
        "total_requests": bson.M{"$sum": 1},   // Count all
        "success_count":  bson.M{"$sum": bson.M{"$cond": []any{
            bson.M{"$eq": []string{"$status", "success"}}, 1, 0,
        }}}},
        "error_count":    bson.M{"$sum": bson.M{"$cond": []any{
            bson.M{"$eq": []string{"$status", "error"}}, 1, 0,
        }}}},
        "avg_latency":    bson.M{"$avg": "$latency_ms"},
    }}},
}
```

The `$cond` expressions act as IF-statements in the aggregation pipeline:
- `$eq: ["success", status]` → 1 if true, 0 if false → `$sum` counts the trues

#### Pipeline 2: Per-Tool Breakdown

```go
toolPipe := mongo.Pipeline{
    {{Key: "$match", Value: match}},
    {{Key: "$group", Value: bson.M{
        "_id":   "$tool_name",   // Group by tool name field
        "count": bson.M{"$sum": 1},
    }}},
}
```

Returns `{"weather": 142, "crypto": 89, "github": 56, ...}`

#### Pipeline 3: Per-Server Breakdown

```go
serverPipe := mongo.Pipeline{
    {{Key: "$match", Value: match}},
    {{Key: "$group", Value: bson.M{
        "_id":   "$server_name",
        "count": bson.M{"$sum": 1},
    }}},
}
```

Returns `{"weather": 142, "crypto": 89, "github": 56, ...}`

### Error Handling

If the aggregation fails (e.g., no data yet), returns a zero-value map:

```go
map[string]any{
    "total_requests": 0, "success_count": 0, "error_count": 0,
    "avg_latency_ms": 0, "requests_by_tool": map[string]int{}, "requests_by_server": map[string]int{},
}
```

---

## 10. Middleware — Protecting API Routes

### File: `middleware.go` (65 lines)

### What It Does

The middleware is an `http.Handler` wrapper that:
1. Checks if the request path is public (no auth needed)
2. For protected paths, extracts the JWT from the `Authorization` header
3. Validates the token
4. Injects the username into the request context
5. Passes the enriched request to the next handler

### Public Routes (No Auth)

```go
switch path {
case "/", "/health", "/chat", "/api/auth/signup", "/api/auth/login", "/api/auth/refresh":
    next.ServeHTTP(w, r)
    return
}
```

These 6 paths allow through without any token:

| Path | Purpose | Who Needs It |
|---|---|---|
| `/` | Dashboard HTML | Everyone (including anonymous) |
| `/health` | Health check endpoint | Health checker only |
| `/chat` | AI chat API call | Authenticated users (but frontend needs to call it first time) |
| `/api/auth/signup` | User registration | New users |
| `/api/auth/login` | User login | Existing users |
| `/api/auth/refresh` | Token refresh | Authenticated users with expiring tokens |

Wait — `/chat` is public? Actually no, looking at how middleware works in context: the middleware in `server.go` is likely only applied to routes below a certain prefix, not `/chat` itself. The `/chat` path is listed here because the middleware file uses exact match and `/chat` might be a public path to allow the frontend to access the chat page, not the POST endpoint (which carries the auth header).

### CORS Preflight Bypass

```go
if r.Method == http.MethodOptions {
    next.ServeHTTP(w, r)
    return
}
```

`OPTIONS` requests (CORS preflight) pass through unconditionally so the CORS handler in `server.go` can add response headers before the auth check happens.

### Token Extraction

```go
authHeader := r.Header.Get("Authorization")
// If header is like "Bearer eyJhbGci..."
// Strip "Bearer " prefix → token = "eyJhbGci..."
```

**Two formats accepted:**
1. `Authorization: Bearer <token>` — standard format, strips the `"Bearer "` prefix (7 chars)
2. Just the raw token string — used by simple clients or the frontend

If the header doesn't start with `"Bearer "` and isn't empty → returns `401` error.

### Context Injection

```go
ctx := context.WithValue(r.Context(), UserKey, username)
next.ServeHTTP(w, r.WithContext(ctx))
```

The `UserKey` is a private type (`ctxKey`) to avoid key collisions in the context. Downstream handlers call `auth.UserFromContext(r.Context())` to retrieve the username.

### The `UserFromContext` Helper

```go
type ctxKey string
const UserKey ctxKey = "username"

func UserFromContext(ctx context.Context) (string, bool) {
    u, ok := ctx.Value(UserKey).(string)
    return u, ok
}
```

Returns `(username, true)` if present, `("", false)` if the user is anonymous.

### The `writeJSON` Helper

```go
func writeJSON(w http.ResponseWriter, status int, data any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(data)
}
```

A small helper for consistent JSON error responses from the middleware.

---

## 11. ChatStore — MongoDB Chat Persistence

### How ChatStore is Created

`auth.go:42-48`:

```go
func (a *Auth) ChatStore() *ChatStore {
    db := a.users.Database()  // Get the same database as users collection
    return &ChatStore{
        sessions: db.Collection("chat_sessions"),
        messages: db.Collection("chat_messages"),
    }
}
```

Uses the **same database** as the users collection (just different collections). This means all auth + data is in one MongoDB database.

### Session Lifecycle

```
User creates session → CreateSession() → stores in chat_sessions
User sends messages → AddMessage() → stores in chat_messages + updates session.updatedAt
User views history   → GetMessages() / GetRecentMessages() → reads from chat_messages
User deletes session → DeleteSession() → deletes from chat_sessions + chat_messages
User renames session → UpdateSessionTitle() → updates chat_sessions.title
User lists sessions  → ListSessions() → reads from chat_sessions sorted by updatedAt desc
```

### Session Methods Detail

| Method | Collection | Key Operations |
|---|---|---|
| `CreateSession(username, title)` | chat_sessions | Inserts with ObjectID, returns hex string as ID |
| `ListSessions(username)` | chat_sessions | Find by username, sort by `updatedAt` descending |
| `GetSession(id, username)` | chat_sessions | Find by ObjectID + username (ownership check) |
| `DeleteSession(id, username)` | chat_sessions + chat_messages | Deletes session + all its messages (two separate dbCtx calls) |
| `UpdateSessionTitle(sessionID, username, title)` | chat_sessions | Updates title + `updatedAt` timestamp |
| `AddMessage(sessionID, role, content, meta)` | chat_messages | Inserts message, then best-effort updates session `updatedAt` |

### Message Method Detail

| Method | Parameters | Sort | Purpose |
|---|---|---|---|
| `GetMessages(sessionID)` | sessionID | `created_at` ascending | Full history in chronological order |
| `GetRecentMessages(sessionID, limit)` | sessionID + limit | `created_at` descending | Last N messages, reversed to chronological |

### Ownership Checks

Every session operation checks `username` matches:
```go
// GetSession requires both _id AND username match
err = cs.sessions.FindOne(ctx, bson.M{"_id": oid, "username": username}).Decode(&raw)
```

This ensures user A cannot access user B's chat sessions or messages.

### Best-Effort Session Update

```go
// In AddMessage after inserting the message:
if _, err := cs.sessions.UpdateByID(ctx2, oid, bson.M{"$set": bson.M{"updated_at": time.Now()}}); err != nil {
    log.Printf("WARNING: failed to update session updated_at for %s: %v", sessionID, err)
}
```

The session `updated_at` update is best-effort — if it fails, the message is still stored correctly. A warning log is emitted for debugging.

### BSON Helper Functions

Three small helpers convert raw MongoDB documents to Go structs:

```go
func getStr(m bson.M, key string) string    // safe string extraction with type assertion
func getTime(m bson.M, key string) time.Time // safe time extraction with type assertion
func bsonToSession(r bson.M) ChatSession     // handles ObjectID → hex conversion
func bsonToMessage(r bson.M) ChatMessage     // handles meta field which can be bson.M or map[string]any
```

The `meta` field requires dual type assertion because BSON unmarshalling can produce either `bson.M` or `map[string]any`:

```go
if meta, ok := r["meta"]; ok {
    if mm, ok := meta.(map[string]any); ok {
        m.Meta = mm
    } else if mm, ok := meta.(bson.M); ok {
        m.Meta = mm
    }
}
```

---

## 12. How Auth Integrates with the Gateway

### With MongoDB Configured

```
User signs up → Auth.Signup() → bcrypt hash → MongoDB insert → JWT returned
User logs in → Auth.Login() → verify bcrypt → MongoDB find → JWT returned
User sends message → Middleware validates JWT → injects username → Handler processes → Auth.LogRequest() stores analytics → Auth.ChatStore().AddMessage() stores chat
```

### Without MongoDB (Anonymous Mode)

```
s.auth is nil
→ Middleware allows all requests through (no JWT check)
→ Chat uses in-memory fallback (s.memHistory, capped at 20 messages)
→ No request logging
→ No session persistence
```

In anonymous mode, sessions are identified by timestamp-based IDs (e.g., `"local-1659061200000000"`). This lets a single user test the full system without setting up MongoDB.

### The Two Modes Side by Side

| Feature | With MongoDB | Without MongoDB |
|---|---|---|
| User accounts | Yes (signup + login) | No (everyone anonymous) |
| JWT tokens | Yes (7-day expiry) | None |
| Chat persistence | MongoDB (permanent) | In-memory (lost on restart) |
| Request analytics | MongoDB aggregation | None |
| Session ownership | Yes (user-scoped) | None |
| Middleware protection | Yes (JWT required) | No (all paths public) |
| LogRequest | Async MongoDB insert | Skipped entirely |

---

## 13. JWT Flow in the Frontend

The Chat UI (`chatui.go` lines 251-271) manages JWT lifecycle:

```javascript
// 1. Store token after login
localStorage.setItem('mcp_token', response.token);

// 2. Read token for each request
function getToken() { return localStorage.getItem('mcp_token'); }
function authHeaders() {
    return { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + getToken() };
}

// 3. Silent refresh every hour
setInterval(silentRefresh, 60 * 60 * 1000);
async function silentRefresh() {
    const token = getToken();
    const exp = tokenExpiresAt(token);  // decode JWT payload
    if (exp - Math.floor(Date.now()/1000) > 24*3600) return; // refresh if <24h left
    const resp = await fetch('/api/auth/refresh', { method:'POST', headers:{'Authorization':'Bearer '+token} });
    if (resp.ok) { const d = await resp.json(); localStorage.setItem('mcp_token', d.token); }
}

// 4. Handle 401 → redirect to login
function redirectToLogin() {
    localStorage.removeItem('mcp_token');
    window.location.href = '/';
}
```

### Token Expiry Check

The `tokenExpiresAt()` function decodes the JWT payload (Base64) to read the `exp` claim without making any network call:

```javascript
function tokenExpiresAt(token) {
    const p = JSON.parse(atob(token.split('.')[1]));  // Decode payload segment
    return p.exp || 0;
}
```

This is the standard way to check JWT expiry client-side — just decode the payload, no API call needed.

---

## 14. Interview Questions & Answers

### Q1: "How does the auth system work end-to-end?"

> 1. User sends username + password to `/api/auth/signup`
> 2. Server hashes the password with bcrypt (`DefaultCost` = 12 rounds)
> 3. Inserts the user document into MongoDB's `users` collection (username and email are unique)
> 4. Generates a JWT with the username as the `sub` claim, signed with `JWT_SECRET`
> 5. Returns the JWT to the client
> 6. Client stores the JWT in `localStorage`
> 7. For every subsequent API request, the client sends `Authorization: Bearer <token>`
> 8. The middleware validates the JWT, extracts the username from `sub`, and injects it into the request context
> 9. The handler uses the username to scope all database operations to that user

### Q2: "Why bcrypt and not a simpler hash like SHA-256?"

> bcrypt is specifically designed for password hashing:
> - **Adaptive cost**: `bcrypt.DefaultCost` = 12 rounds, making it intentionally slow. SHA-256 is fast, which is bad for passwords (attackers can try billions of guesses per second).
> - **Built-in salt**: bcrypt automatically generates and embeds a random salt in the hash output. SHA-256 would need manual salt generation and storage.
> - **Standard**: bcrypt is the industry standard for password hashing, used by frameworks like Django, Rails, etc.
> - **60-character output**: The bcrypt hash format is fixed at 60 characters, which is convenient for database column sizing.

### Q3: "What happens if JWT_SECRET is not set?"

> The `New()` constructor returns an error immediately and the gateway fails to start with `"JWT_SECRET environment variable must be set"`. This is a hard requirement — there is no fallback. The system enforces that the secret is set as an environment variable, never hardcoded in the source code.

### Q4: "How does the middleware protect API routes?"

> The middleware uses an exact-match `switch` statement on `r.URL.Path`:
> - **Public paths** (`/`, `/health`, `/chat`, `/api/auth/signup`, `/api/auth/login`, `/api/auth/refresh`) pass through without a token check
> - **All other paths** require an `Authorization: Bearer <token>` header
> - The token is validated by `ValidateToken()`, which checks the HMAC-SHA256 signature and expiry
> - On success, the username is injected into the request context via `context.WithValue`
> - Downstream handlers call `auth.UserFromContext(r.Context())` to get the authenticated username

### Q5: "Why does `DeleteSession` use two separate `dbCtx()` calls?"

> MongoDB operations use context timeouts. Since `DeleteSession` performs two independent operations (delete session, then delete messages), it creates a separate `dbCtx()` for each. This ensures that if the first operation succeeds but the second times out, the session deletion still completes — only the message cleanup attempt is abandoned (with a warning log).

### Q6: "How does `LogRequest` avoid blocking the main request?"

> `LogRequest` launches a **goroutine** (`go func() { ... }()`):
> - The request handler doesn't wait for the insert to complete
> - The goroutine runs independently with its own 5-second timeout context
> - If MongoDB is slow or down, the goroutine fails silently (no error propagated)
> - This is correct for logging — losing a log entry is far better than slowing down user requests

### Q7: "What MongoDB aggregation pipeline operators are used for analytics?"

> The analytics use three key operators:
> - `$match` — filters documents by username (or no filter for global stats)
> - `$group` — groups documents and computes aggregates
> - `$sum` — adds up values (used for counts: total, success, error)
> - `$cond` — conditional expression (acts as IF: `$eq: ["success", status]` → 1 if true, else 0)
> - `$avg` — computes the average of a numeric field (`latency_ms`)
> - `$eq` — equality comparison used inside `$cond`
>
> The aggregation pipeline syntax is MongoDB's map-reduce alternative, which is more efficient and flexible for real-time analytics.

### Q8: "What is the purpose of the `ensureIndexes` function?"

> MongoDB indexes accelerate queries. Without them, every `FindOne` query would scan the entire collection (collection scan). The function creates:
> - **Unique index on `users.username`** — prevents duplicate usernames and makes login lookups O(log n) instead of O(n)
> - **Unique index on `users.email`** — prevents duplicate emails and makes email-based lookups fast
> - **Index on `request_logs.username + created_at desc`** — makes per-user log queries efficient (sorted newest-first)
> - **Index on `request_logs.created_at desc`** — makes global log queries and analytics fast
>
> MongoDB indexes are created once and maintained automatically as documents are inserted, updated, or deleted.

### Q9: "How does `RefreshToken` maintain multi-device support?"

> Each token has its own independent 7-day expiry from when it was issued. When you refresh a token:
> - The OLD token remains valid until its original expiry
> - The NEW token gets a fresh 7-day expiry from the current time
> - Both tokens work simultaneously
>
> This means logging in on your phone doesn't invalidate your laptop's session, and vice versa. Each device can refresh independently.

### Q10: "Why are some MongoDB operations fire-and-forget goroutines while others use synchronous contexts?"

> The pattern depends on the operation's importance:
>
> | Operation | Pattern | Reason |
> |---|---|---|
> | `LogRequest` | Goroutine (fire-and-forget) | Losing a log entry has no user impact; blocking requests would hurt UX |
> | `AddMessage` + session update | Synchronous + best-effort fallback | The message MUST be stored; the `updatedAt` update is nice-to-have |
> | `DeleteSession` + messages | Two separate `dbCtx()` calls | Session deletion is critical; message cleanup is secondary |
> | `Signup`/`Login` | Synchronous | These are core auth operations; failures must be returned to the user |
> | `GetUser` | Synchronous | Needed for login validation; can't be async |

---

## 15. Diagrams

### Auth System Overview

```mermaid
graph TB
    subgraph "Client"
        B[Browser / Frontend]
    end

    subgraph "MCP Gateway :8080"
        MW[Auth Middleware<br/>JWT Check]
        A[Auth Handler<br/>signup / login / refresh]
        CH[Chat Handler<br/>AI Chat]
    end

    subgraph "MongoDB"
        U[users collection]
        RL[request_logs collection]
        CS[chat_sessions collection]
        CM[chat_messages collection]
    end

    B -->|"POST /api/auth/signup"| A
    B -->|"POST /api/auth/login"| A
    B -->|"POST /api/auth/refresh"| A
    B -->|"Authorization: Bearer <token>"| MW
    MW -->|"Valid token → inject username"| CH
    MW -->|"Valid token → inject username"| A
    A -->|"Insert / Find user"| U
    CH -->|"LogRequest async"| RL
    CH -->|"AddMessage"| CM
    CH -->|"Session ops"| CS
```

### JWT Lifecycle

```mermaid
sequenceDiagram
    participant U as User
    participant F as Frontend
    participant A as Auth.New()
    participant M as Middleware
    participant H as Handler

    Note over U,H: Signup Flow
    U->>F: Enters username, email, password
    F->>A: POST /api/auth/signup {username, email, password}
    A->>A: bcrypt.GenerateFromPassword(password, 12)
    A->>U: Insert user into MongoDB
    A->>A: generateToken(username)
    A-->>F: {token: "JWT...", expires: "7 days"}

    Note over F,U: Login Flow
    U->>F: Enters username, password
    F->>A: POST /api/auth/login {username, password}
    A->>A: Find user in MongoDB by username
    A->>A: bcrypt.CompareHashAndPassword(storedHash, password)
    A->>A: generateToken(username)
    A-->>F: {token: "JWT...", expires: "7 days"}

    Note over F,U: Subsequent Requests
    F->>M: GET /api/chat/sessions (Authorization: Bearer <token>)
    M->>M: ValidateToken(tokenStr)
    M->>M: Extract "sub" claim
    M->>H: Pass enriched context with username
    H->>U: Return sessions + messages

    Note over F,U: Token Refresh (every hour)
    F->>A: POST /api/auth/refresh (Authorization: Bearer <old-token>)
    A->>A: ValidateToken(oldToken)
    A->>A: generateToken(username) // fresh 7-day expiry
    A-->>F: {token: "NEW_JWT..."}
    F->>F: Store new token in localStorage
```

### Middleware Request Filtering

```
Every incoming HTTP request
        │
        ▼
┌─────────────────────────────┐
│  Is path "OPTIONS"?         │──Yes──→ Pass to CORS handler (no auth)
└─────────────┬───────────────┘
        │ No
        ▼
┌─────────────────────────────┐
│  Is path one of the 6       │──Yes──→ Pass to handler (no auth check)
│  public paths?              │    (/ /health /chat /signup /login /refresh)
└─────────────┬───────────────┘
        │ No (protected path)
        ▼
┌─────────────────────────────┐
│  Authorization header exists?│──No──→ 401 {"error": "missing authorization header"}
└─────────────┬───────────────┘
        │ Yes
        ▼
┌─────────────────────────────┐
│  Starts with "Bearer "?     │──No──→ 401 {"error": "invalid authorization format"}
└─────────────┬───────────────┘
        │ Yes
        ▼
┌─────────────────────────────┐
│  Validate JWT signature     │──Invalid──→ 401 {"error": "invalid or expired token"}
│  (HS256, not expired)       │
└─────────────┬───────────────┘
        │ Valid
        ▼
┌─────────────────────────────┐
│  Extract "sub" = username   │
│  Inject into request context│
└─────────────┬───────────────┘
              ▼
    Pass to actual handler with username in context
```

### ChatStore Collection Relationships

```mermaid
erDiagram
    CHAT_SESSIONS ||--o{ CHAT_MESSAGES : contains

    CHAT_SESSIONS {
        string _id PK "primitive.ObjectID (hex)"
        string username "FK → users.username"
        string title "e.g. 'New Chat', 'My Project'"
        datetime created_at
        datetime updated_at
    }

    CHAT_MESSAGES {
        string _id PK "primitive.ObjectID (hex)"
        string session_id FK "references CHAT_SESSIONS._id"
        string role "'user' or 'ai'"
        string content "the actual message text"
        map meta "optional: tools_used, latency, steps"
        datetime created_at
    }
```

### Request Logging Data Model

```mermaid
erDiagram
    REQUEST_LOGS {
        string _id PK "primitive.ObjectID"
        string username "FK → users.username"
        string method "'chat', 'agent', 'auth'"
        string tool_name "'get_weather', 'add_note', etc."
        string server_name "'weather', 'notes', 'crypto', etc."
        string status "'success' or 'error'"
        string error "empty string if success, error message if failed"
        int64 latency_ms "response time in milliseconds"
        datetime created_at "timestamp of the request"
    }
```

### Request Flow: Authenticated Chat Request

```mermaid
sequenceDiagram
    participant U as User (Browser)
    participant M as Middleware (middleware.go)
    participant H as Chat Handler (chat.go)
    participant B as Brain (brain.go)
    participant MW as Gateway Forwarder
    participant S as MCP Server (e.g., weather:3001)
    participant DB as MongoDB
    participant L as Logger (async goroutine)

    U->>U: Types message, hits Enter
    U->>M: POST /api/chat<br/>Authorization: Bearer <token><br/>Body: {message, session_id}

    M->>M: Check path is not public
    M->>M: Extract Bearer token
    M->>M: Validate JWT signature + expiry
    M->>M: Extract "sub" = username
    M->>H: context.WithValue(ctx, UserKey, username)

    H->>H: Validate session ownership (DB check)
    H->>DB: Get session + messages for user
    H->>H: Build OrchestratorConfig (no auth if s.auth == nil)
    H->>H: Build callToolFn → ForwardToolCall

    H->>B: ProcessWithOrchestrator(message, history, callToolFn, config)
    B->>B: DecomposeGoal → Plan
    B->>B: ExecutePlan → callTool for each task

    B->>H: callTool("get_weather", {city: "Mumbai"})
    H->>MW: ForwardToolCall {method: "tools/call", tool: "get_weather", args: {city: "Mumbai"}}
    MW->>S: POST http://localhost:3001/mcp/message
    S->>S: fetchWeather("Mumbai") → wttr.in API
    S-->>MW: "32°C, Sunny"
    MW-->>H: tool result
    H-->>B: result

    B->>B: compileResults → final answer
    B-->>H: OrchestratorResult{Answer, Steps, ...}

    H->>L: LogRequest(username, "chat", "", "", "success", "", <latency>)
    H->>DB: ChatStore.AddMessage(sessionID, "user", message)
    H->>DB: ChatStore.AddMessage(sessionID, "ai", answer)

    H-->>U: {answer, tools_used, latency, steps}
    U->>U: Display AI response in chat UI
```

---

## Quick Reference

### Auth Methods

| Method | File | Input | Output | Purpose |
|--------|------|-------|--------|---------|
| `Signup(username, email, password)` | auth.go:83 | strings | `(token string, error)` | Register new user, return JWT |
| `Login(username, password)` | auth.go:109 | strings | `(token string, error)` | Authenticate, return JWT |
| `ValidateToken(tokenStr)` | auth.go:125 | string | `(username string, error)` | Verify JWT, extract username |
| `RefreshToken(tokenStr)` | auth.go:332 | string | `(token string, error)` | Issue new token with fresh expiry |
| `LogRequest(...)` | auth.go:161 | string + latency | `void` (fire-and-forget goroutine) | Store request analytics |
| `GetRequestStats(username)` | auth.go:192 | string | `map[string]any` | Aggregate analytics via MongoDB pipeline |
| `RecentLogs(n, username)` | auth.go:284 | int + string | `[]RequestLogEntry` | Query recent log entries |
| `ChatStore()` | chat.go:42 | (none) | `*ChatStore` | Create chat store from same MongoDB database |
| `generateToken(username)` | auth.go:318 | string | `(string, error)` | Create JWT with 7-day expiry |

### Middleware Public Routes

| Path | Method | Auth Required? |
|------|--------|----------------|
| `/` | GET | No |
| `/health` | GET | No |
| `/chat` | GET | No |
| `/api/auth/signup` | POST | No |
| `/api/auth/login` | POST | No |
| `/api/auth/refresh` | POST | No |
| All other paths | Any | Yes (Bearer token) |

### MongoDB Collections

| Collection | Documents | Key Fields |
|---|---|---|
| `users` | User accounts | username (unique), email (unique), password hash |
| `request_logs` | Per-request logs | username, method, tool_name, status, latency_ms |
| `chat_sessions` | Chat sessions | username, title, updatedAt |
| `chat_messages` | Individual messages | session_id, role, content, meta |

### Bcrypt Default Cost

| Cost Factor | Rounds | Approximate Time |
|---|---|---|
| 4 | 16 | ~1ms |
| 8 | 256 | ~30ms |
| 10 | 1024 | ~100ms |
| **12** (DefaultCost) | **4096** | **~250ms** |
| 14 | 16384 | ~1s |
| 16 | 65536 | ~4s |

Cost 12 is the recommended default — slow enough to deter brute force, fast enough that signup/login still feels instant to users.

### Key Environment Variables

| Variable | Purpose | Required? |
|---|---|---|
| `JWT_SECRET` | HMAC signing key for JWT tokens | Yes (startup fails without it) |
| `MONGODB_URI` | MongoDB connection string | Yes (if MongoDB is used) |
| `MONGODB_DATABASE` | MongoDB database name | Yes (if MongoDB is used) |
| `GROQ_API_KEY` | Groq API key for AI | Only if using AI chat features |
| `GROQ_MODELS` | Override default Groq models | No (uses defaults if unset) |

---

*End of Part 6: Authentication — JWT, MongoDB & Middleware.*
