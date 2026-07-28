# Part 6: Authentication — JWT, MongoDB & Middleware

## 1. Overview

The `auth/` package provides the complete authentication and authorization layer for MCP Gateway. It handles user registration, login (bcrypt password hashing), JWT token issuance/validation, automatic request logging to MongoDB, aggregated request statistics, and chat session persistence. The middleware protects all API endpoints except a small public set (health, static, auth routes).

```
┌──────────┐     POST /api/auth/signup      ┌─────────┐
│  Browser  │ ─────────────────────────────→  │  Auth   │
│  (Frontend)│                                │ (auth/) │
│           │ ←────────────────────────────  │         │
│           │   {token} (JWT, 7-day expiry)  │         │
└──────────┘                                  └────┬────┘
                                                   │
                         ┌─────────────────────────┼─────────────────────────┐
                         ▼                         ▼                         ▼
                   ┌───────────┐           ┌───────────┐           ┌───────────┐
                   │   Auth    │           │  Middleware│           │  ChatStore│
                   │ (signup,  │           │ (JWT check) │           │ (MongoDB) │
                   │  login,   │           └───────────┘           └───────────┘
                   │  tokens)  │
                   └───────────┘
```

**3 files:** `auth.go` (338 lines), `middleware.go` (65 lines), `chat.go` (271 lines)

---

## 2. Auth Core (`auth.go`) — JWT + MongoDB + Request Logging

**File:** `internal/auth/auth.go` (338 lines)

### 2.1 Data Structures

```go
type User struct {
    Username  string    `bson:"username" json:"username"`
    Email     string    `bson:"email" json:"email"`
    Password  string    `bson:"password" json:"-"`    // never serialized to JSON
    CreatedAt time.Time `bson:"created_at" json:"created_at"`
}

type Auth struct {
    users       *mongo.Collection   // "users" collection
    requestLogs *mongo.Collection   // "request_logs" collection
    jwtSecret   []byte              // from JWT_SECRET env var
    db          *mongo.Database
}
```

### 2.2 Constructor — `New(mCfg MongoConfig) (*Auth, error)`

1. Connects to MongoDB with a 10-second timeout
2. Pings MongoDB to verify the connection
3. Reads `JWT_SECRET` from environment — fails immediately if not set
4. Gets the configured database
5. Creates unique indexes on `users.username` and `users.email`
6. Creates indexes on `request_logs.username`, `request_logs.created_at` (descending)
7. Returns `*Auth`

```go
type MongoConfig struct {
    URI      string
    Database string
}
```

If `MongoConfig.URI` is empty, the connection string defaults to MongoDB's standard local address.

### 2.3 Signup — `Signup(username, email, password string) (string, error)`

1. Hashes the password using `bcrypt.GenerateFromPassword` with `bcrypt.DefaultCost`
2. Creates a `User` struct with `CreatedAt: time.Now()`
3. Inserts into the `users` collection
4. If duplicate key error (username or email exists) → returns a clear error message
5. On success → calls `generateToken(username)` and returns the JWT

### 2.4 Login — `Login(username, password string) (string, error)`

1. Finds the user by username in MongoDB
2. If user not found → returns `"invalid username or password"` (no distinction between bad user/bad password)
3. Compares the bcrypt hash with the provided password via `bcrypt.CompareHashAndPassword`
4. If mismatch → same `"invalid username or password"` error
5. On success → calls `generateToken(username)` and returns the JWT

### 2.5 Token Generation — `generateToken(username string) (string, error)`

```go
claims := jwt.MapClaims{
    "sub": username,
    "iat": time.Now().Unix(),
    "exp": time.Now().Add(7 * 24 * time.Hour).Unix(), // 7-day token
}
token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
return token.SignedString(a.jwtSecret)
```

Tokens use **HMAC-SHA256** signing with a 7-day expiry. The `sub` (subject) claim holds the username.

### 2.6 Token Validation — `ValidateToken(tokenStr string) (string, error)`

1. Parses the JWT using the `jwtSecret`
2. Validates the signing method is HMAC-SHA256 (rejects unexpected alg)
3. Extracts the `sub` claim as the username string
4. Returns the username on success, or an error on any validation failure

### 2.7 Token Refresh — `RefreshToken(tokenStr string) (string, error)`

1. Validates the existing token via `ValidateToken()`
2. If valid → issues a brand new token with a fresh 7-day expiry
3. Both old and new tokens remain valid independently until expiry
4. This allows simultaneous logins from multiple devices without one logout invalidating others

### 2.8 Request Logging — `LogRequest(...)`

Fire-and-forget goroutine:

```go
func (a *Auth) LogRequest(username, method, toolName, serverName, status, errMsg string, latency time.Duration) {
    go func() {
        // Insert into request_logs collection with all fields
    }()
}
```

Logged fields: `username`, `method`, `tool_name`, `server_name`, `status` ("success"/"error"), `error`, `latency_ms`, `created_at`

### 2.9 Request Analytics — `GetRequestStats(username string) map[string]any`

Uses MongoDB aggregation pipelines to compute:
- **Totals:** `total_requests`, `success_count`, `error_count`, `avg_latency_ms`
- **Per-tool breakdown:** `$group` by `tool_name` with count
- **Per-server breakdown:** `$group` by `server_name` with count
- Pass empty string for username to get global (all users) stats

### 2.10 Recent Logs — `RecentLogs(n int, username string) []RequestLogEntry`

Returns the last N log entries sorted by `created_at` descending, filtered by username if provided. Converts `bson.M` documents to `RequestLogEntry` structs with proper type assertions for `latency_ms`.

---

## 3. Middleware (`middleware.go`) — JWT Protection

**File:** `internal/auth/middleware.go` (65 lines)

### 3.1 Public Routes (No Auth Required)

The middleware allows these paths through without a token:

| Path | Purpose |
|---|---|
| `/` | Dashboard HTML |
| `/health` | Health check endpoint |
| `/chat` | AI Chat endpoint (the frontend calls this) |
| `/api/auth/signup` | User registration |
| `/api/auth/login` | User login |
| `/api/auth/refresh` | JWT token refresh |

**Exact match only:** Uses a `switch` statement, not `HasPrefix`, so `/` doesn't accidentally match every path.

### 3.2 Protected Routes

All other paths require a valid `Authorization` header.

### 3.3 Token Extraction

1. Reads `Authorization` header
2. If empty → 401 Unauthorized with `{"error": "missing authorization header"}`
3. If starts with `"Bearer "` → strips the prefix to get the raw token
4. If doesn't start with `"Bearer "` and isn't empty → 401 with `{"error": "invalid authorization format"}`
5. Validates the JWT via `a.ValidateToken(token)`
6. On failure → 401 with `{"error": "invalid or expired token"}`
7. On success → injects username into request context via `context.WithValue(r.Context(), UserKey, username)` and passes to the next handler

### 3.4 Context Extraction — `UserFromContext(ctx context.Context) (string, bool)`

Extracts the username from the request context. Returns the username string and a boolean indicating whether it was found. Used by `chat.go` and the dashboard handler to identify the authenticated user.

### 3.5 CORS Preflight Bypass

`OPTIONS` requests are passed through immediately without auth checks, allowing the CORS handler in `server.go` to add the appropriate response headers.

---

## 4. Chat Store (`chat.go`) — MongoDB Chat Persistence

**File:** `internal/auth/chat.go` (271 lines)

### 4.1 Data Types

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
    Role      string         `json:"role"`       // "user" or "ai"
    Content   string         `json:"content"`
    Meta      map[string]any `json:"meta,omitempty"`  // tool names, latency, etc.
    CreatedAt time.Time      `json:"created_at"`
}
```

### 4.2 ChatStore — MongoDB Collections

`Auth.ChatStore()` creates a `ChatStore` using the same MongoDB database (different collections):
- `chat_sessions` — session metadata (id, user, title, timestamps)
- `chat_messages` — individual messages with role, content, and optional metadata

### 4.3 Session CRUD Operations

| Method | Operation | Key Detail |
|---|---|---|
| `CreateSession(username, title)` | Inserts into `chat_sessions` | Uses `primitive.NewObjectID()` for `_id`, returns session with `ID = oid.Hex()` |
| `ListSessions(username)` | Finds all sessions for user, sorted by `updated_at` descending | Returns most recently updated first |
| `GetSession(id, username)` | Finds session by ObjectID + username (ownership check) | Returns error if session doesn't belong to user |
| `DeleteSession(id, username)` | Deletes session + all its messages | Uses two separate dbCtx() calls; logs warning if message deletion fails |
| `UpdateSessionTitle(sessionID, username, title)` | Updates title and `updated_at` | Ownership-checked update |

### 4.4 Message Operations

| Method | Operation | Key Detail |
|---|---|---|
| `AddMessage(sessionID, role, content, meta)` | Inserts message, then best-effort updates session `updated_at` | Best-effort update (logs warning on failure, doesn't fail the message insert) |
| `GetMessages(sessionID)` | Finds all messages for a session, sorted chronologically (`created_at` ascending) | Returns in natural conversation order |
| `GetRecentMessages(sessionID, limit)` | Finds messages sorted by `created_at` descending, then reverses | Returns in chronological order (most recent last) |

### 4.5 BSON Helper Functions

Three helper functions convert raw `bson.M` documents to Go structs:
- `bsonToSession(r bson.M) ChatSession` — extracts ID (ObjectID → hex), username, title, timestamps
- `bsonToMessage(r bson.M) ChatMessage` — handles the `meta` field which can be either `bson.M` or `map[string]any`
- `getStr(m bson.M, key string) string` — safe string extraction with type assertion
- `getTime(m bson.M, key string) time.Time` — safe time extraction with type assertion

---

## 5. How Auth Integrates with the Rest of the System

### 5.1 Server Setup (`server.go`)

When MongoDB is configured:
1. `auth.New(mongoConfig)` is called → connects to MongoDB, creates collections
2. `auth.Middleware` wraps all protected routes
3. `auth.ChatStore()` is passed to the chat handler for session persistence
4. `auth.RequestLogs` are used for the analytics dashboard
5. `auth.LogRequest(...)` is called after each tool call and each chat request

When MongoDB is NOT configured:
- `s.auth` is `nil`
- The middleware allows all requests through (no auth)
- Chat uses in-memory fallback (`s.memHistory`)
- Request logging is skipped entirely
- The system works as an anonymous, single-user tool

### 5.2 JWT in the Frontend (`chatui.go`)

The chat UI stores the JWT in `localStorage` as `mcp_token`:
- Sent as `Authorization: Bearer <token>` header on every request
- Auto-refreshed silently every hour via `/api/auth/refresh`
- On 401 response → clears localStorage and redirects to login

### 5.3 Request Flow with Auth

```
1. User signs up → POST /api/auth/signup → bcrypt hash → insert user → return JWT
2. User logs in → POST /api/auth/login → verify bcrypt → return JWT
3. User sends chat message:
   a. Frontend: GET /api/chat/sessions (with Bearer token)
   b. Middleware checks token → extracts username → passes to handler
   c. Handler loads user's chat sessions from MongoDB
   d. User sends message → POST /api/chat (with Bearer token + session_id)
   e. Middleware validates token → handler processes via orchestrator
   f. Handler logs the request: auth.LogRequest(username, "chat", ...)
   g. Handler stores the AI reply in chat_messages collection
4. Every 60 minutes, frontend silently refreshes the JWT
```

---

## 6. Security Considerations

| Aspect | Implementation |
|---|---|
| Password hashing | bcrypt with `DefaultCost` (adaptive, salted automatically) |
| Token signing | HMAC-SHA256 with secret from `JWT_SECRET` env var |
| Token expiry | 7 days, sliding on refresh |
| No user/enquiry leak | Login/signup return generic `"invalid username or password"` for all failures |
| Session ownership | `GetSession`/`DeleteSession`/`UpdateSessionTitle` all check `username` matches |
| Fire-and-forget logging | `LogRequest` runs in a goroutine so logging failures don't affect the request |
| Context timeout | All MongoDB operations have a 10s context timeout |
| Secret never logged | Password field has `json:"-"` tag to exclude from JSON serialization |