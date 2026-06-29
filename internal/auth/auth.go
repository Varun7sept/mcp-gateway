package auth

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	Username  string    `bson:"username" json:"username"`
	Email     string    `bson:"email" json:"email"`
	Password  string    `bson:"password" json:"-"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
}

type Auth struct {
	users       *mongo.Collection
	requestLogs *mongo.Collection
	jwtSecret   []byte
	db          *mongo.Database
}

type MongoConfig struct {
	URI      string
	Database string
}

func New(mCfg MongoConfig) (*Auth, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mCfg.URI))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}

	if err := client.Ping(ctx, nil); err != nil {
		return nil, fmt.Errorf("mongo ping: %w", err)
	}

	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return nil, fmt.Errorf("JWT_SECRET environment variable must be set")
	}

	db := client.Database(mCfg.Database)

	a := &Auth{
		users:       db.Collection("users"),
		requestLogs: db.Collection("request_logs"),
		jwtSecret:   []byte(secret),
		db:          db,
	}

	if err := a.ensureIndexes(ctx); err != nil {
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}

	return a, nil
}

func (a *Auth) ensureIndexes(ctx context.Context) error {
	if _, err := a.users.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "username", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)},
	}); err != nil {
		return err
	}
	_, err := a.requestLogs.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "username", Value: 1}, {Key: "created_at", Value: -1}}},
		{Keys: bson.D{{Key: "created_at", Value: -1}}},
	})
	return err
}

func (a *Auth) Signup(username, email, password string) (string, error) {
	ctx := context.Background()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	user := User{
		Username:  username,
		Email:     email,
		Password:  string(hash),
		CreatedAt: time.Now(),
	}

	if _, err := a.users.InsertOne(ctx, user); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return "", fmt.Errorf("username or email already exists")
		}
		return "", fmt.Errorf("insert user: %w", err)
	}

	return a.generateToken(username)
}

func (a *Auth) Login(username, password string) (string, error) {
	ctx := context.Background()
	var user User
	err := a.users.FindOne(ctx, bson.M{"username": username}).Decode(&user)
	if err != nil {
		return "", fmt.Errorf("invalid username or password")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return "", fmt.Errorf("invalid username or password")
	}

	return a.generateToken(username)
}

func (a *Auth) ValidateToken(tokenStr string) (string, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.jwtSecret, nil
	})
	if err != nil {
		return "", fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token")
	}

	username, ok := claims["sub"].(string)
	if !ok {
		return "", fmt.Errorf("invalid token claims")
	}

	return username, nil
}

func (a *Auth) GetUser(username string) (*User, error) {
	ctx := context.Background()
	var user User
	err := a.users.FindOne(ctx, bson.M{"username": username}).Decode(&user)
	if err != nil {
		return nil, fmt.Errorf("user not found")
	}
	return &user, nil
}

// LogRequest stores a request log entry in MongoDB.
func (a *Auth) LogRequest(username, method, toolName, serverName, status, errMsg string, latency time.Duration) {
	ctx := context.Background()
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
}

// RequestLogEntry represents a stored request log.
type RequestLogEntry struct {
	Username   string    `json:"username"`
	Method     string    `json:"method"`
	ToolName   string    `json:"tool_name"`
	ServerName string    `json:"server_name"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	LatencyMs  int64     `json:"latency_ms"`
	CreatedAt  time.Time `json:"created_at"`
}

// GetRequestStats returns aggregate stats for a user from MongoDB.
// Pass empty username to get global stats.
func (a *Auth) GetRequestStats(username string) map[string]any {
	ctx := context.Background()
	match := bson.M{}
	if username != "" {
		match = bson.M{"username": username}
	}

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.M{
			"_id":           nil,
			"total_requests": bson.M{"$sum": 1},
			"success_count":  bson.M{"$sum": bson.M{"$cond": []any{bson.M{"$eq": []string{"$status", "success"}}, 1, 0}}},
			"error_count":    bson.M{"$sum": bson.M{"$cond": []any{bson.M{"$eq": []string{"$status", "error"}}, 1, 0}}},
			"avg_latency":    bson.M{"$avg": "$latency_ms"},
		}}},
	}

	cursor, err := a.requestLogs.Aggregate(ctx, pipeline)
	if err != nil {
		return map[string]any{
			"total_requests": 0, "success_count": 0, "error_count": 0, "avg_latency_ms": 0,
			"requests_by_tool": map[string]int{}, "requests_by_server": map[string]int{},
		}
	}
	var results []bson.M
	cursor.All(ctx, &results)

	stats := map[string]any{
		"total_requests":    0,
		"success_count":     0,
		"error_count":       0,
		"avg_latency_ms":    0,
		"requests_by_tool":  map[string]int{},
		"requests_by_server": map[string]int{},
	}
	if len(results) > 0 {
		r := results[0]
		if v, ok := r["total_requests"]; ok { stats["total_requests"] = v }
		if v, ok := r["success_count"]; ok { stats["success_count"] = v }
		if v, ok := r["error_count"]; ok { stats["error_count"] = v }
		if v, ok := r["avg_latency"]; ok { stats["avg_latency_ms"] = v }
	}

	// Per-tool breakdown
	toolPipe := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.M{
			"_id":   "$tool_name",
			"count": bson.M{"$sum": 1},
		}}},
	}
	if tc, err := a.requestLogs.Aggregate(ctx, toolPipe); err == nil {
		var tres []bson.M
		tc.All(ctx, &tres)
		tmap := map[string]int{}
		for _, r := range tres {
			if name, ok := r["_id"].(string); ok && name != "" {
				if c, ok := r["count"].(int32); ok { tmap[name] = int(c) }
			}
		}
		stats["requests_by_tool"] = tmap
	}

	// Per-server breakdown
	serverPipe := mongo.Pipeline{
		{{Key: "$match", Value: match}},
		{{Key: "$group", Value: bson.M{
			"_id":   "$server_name",
			"count": bson.M{"$sum": 1},
		}}},
	}
	if sc, err := a.requestLogs.Aggregate(ctx, serverPipe); err == nil {
		var sres []bson.M
		sc.All(ctx, &sres)
		smap := map[string]int{}
		for _, r := range sres {
			if name, ok := r["_id"].(string); ok && name != "" {
				if c, ok := r["count"].(int32); ok { smap[name] = int(c) }
			}
		}
		stats["requests_by_server"] = smap
	}

	return stats
}

// RecentLogs returns the last N log entries for a user from MongoDB.
func (a *Auth) RecentLogs(n int, username string) []RequestLogEntry {
	ctx := context.Background()
	filter := bson.M{}
	if username != "" {
		filter = bson.M{"username": username}
	}
	cursor, err := a.requestLogs.Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(int64(n)))
	if err != nil {
		return nil
	}
	var raw []bson.M
	cursor.All(ctx, &raw)
	entries := make([]RequestLogEntry, 0, len(raw))
	for _, r := range raw {
		e := RequestLogEntry{
			Username:   getStr(r, "username"),
			Method:     getStr(r, "method"),
			ToolName:   getStr(r, "tool_name"),
			ServerName: getStr(r, "server_name"),
			Status:     getStr(r, "status"),
			Error:      getStr(r, "error"),
			CreatedAt:  getTime(r, "created_at"),
		}
		if v, ok := r["latency_ms"]; ok {
			if n, ok := v.(int64); ok { e.LatencyMs = n }
		}
		entries = append(entries, e)
	}
	return entries
}

func (a *Auth) generateToken(username string) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.jwtSecret)
}
