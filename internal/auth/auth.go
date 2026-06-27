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
	users     *mongo.Collection
	jwtSecret []byte
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
		secret = "mcp-gateway-secret-change-in-production"
	}

	a := &Auth{
		users:     client.Database(mCfg.Database).Collection("users"),
		jwtSecret: []byte(secret),
	}

	if err := a.ensureIndexes(ctx); err != nil {
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}

	return a, nil
}

func (a *Auth) ensureIndexes(ctx context.Context) error {
	_, err := a.users.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "username", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)},
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

func (a *Auth) generateToken(username string) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.jwtSecret)
}
