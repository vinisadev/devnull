package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	deleteAfter = 2 * time.Minute
)

type Message struct {
	gorm.Model
	MessageID  string
	ChannelID  string
	Content    string
	AuthorID   string
	DeleteTime time.Time
}

var db *gorm.DB

func initDB() {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
		os.Getenv("DB_HOST"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"),
		os.Getenv("DB_PORT"),
	)

	var err error
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Auto migrate the Message model
	err = db.AutoMigrate(&Message{})
	if err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}
}

func main() {
	// Initialize database
	initDB()

	// Get bot token from environment variable
	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_BOT_TOKEN environment variable not set")
	}

	// Create Discord session
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatalf("Error creating Discord session: %v", err)
	}

	// Register message handler
	dg.AddHandler(messageCreate)

	// Open connection
	err = dg.Open()
	if err != nil {
		log.Fatalf("Error opening connection: %v", err)
	}
	defer dg.Close()

	// Wait for interrupt signal
	fmt.Println("Bot is now running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore bot's own messages
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Store message in database
	msg := Message{
		MessageID:  m.ID,
		ChannelID:  m.ChannelID,
		Content:    m.Content,
		AuthorID:   m.Author.ID,
		DeleteTime: time.Now().Add(deleteAfter),
	}

	if err := db.Create(&msg).Error; err != nil {
		log.Printf("Error saving message to database: %v", err)
	}

	// Schedule message deletion
	go func() {
		time.Sleep(deleteAfter)
		err := s.ChannelMessageDelete(m.ChannelID, m.ID)
		if err != nil {
			log.Printf("Error deleting message: %v", err)
		}
	}()
}
