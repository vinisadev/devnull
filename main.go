package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
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

type ChannelSettings struct {
	gorm.Model
	ChannelID string `gorm:"uniqueIndex"`
	ServerID  string
	Enabled   bool
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

	// Auto migrate models
	err = db.AutoMigrate(&Message{}, &ChannelSettings{})
	if err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	// Create default settings for existing channels (disabled by default)
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

	// Register handlers
	dg.AddHandler(messageCreate)
	dg.AddHandler(messageCommand)

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

func messageCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore bot's own messages
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Check for !autodelete command
	if !strings.HasPrefix(m.Content, "!autodelete") {
		return
	}

	// Only allow server admins to use this command
	member, err := s.GuildMember(m.GuildID, m.Author.ID)
	if err != nil || !hasAdminPermissions(s, member) {
		return
	}

	parts := strings.Fields(m.Content)
	if len(parts) < 2 {
		s.ChannelMessageSend(m.ChannelID, "Usage: !autodelete [enable|disable]")
		return
	}

	var settings ChannelSettings
	result := db.FirstOrCreate(&settings, ChannelSettings{ChannelID: m.ChannelID, ServerID: m.GuildID})
	if result.Error != nil {
		log.Printf("Error getting channel settings: %v", result.Error)
		return
	}

	switch parts[1] {
	case "enable":
		settings.Enabled = true
		s.ChannelMessageSend(m.ChannelID, "Auto-delete enabled for this channel")
	case "disable":
		settings.Enabled = false
		s.ChannelMessageSend(m.ChannelID, "Auto-delete disabled for this channel")
	default:
		s.ChannelMessageSend(m.ChannelID, "Usage: !autodelete [enable|disable]")
		return
	}

	db.Save(&settings)
}

func hasAdminPermissions(s *discordgo.Session, member *discordgo.Member) bool {
	// Check for administrator permission
	for _, roleID := range member.Roles {
		role, err := s.State.Role(member.GuildID, roleID)
		if err != nil {
			continue
		}
		if role.Permissions&discordgo.PermissionAdministrator != 0 {
			return true
		}
	}
	return false
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore bot's own messages and commands
	if m.Author.ID == s.State.User.ID || strings.HasPrefix(m.Content, "!") {
		return
	}

	// Check if auto-delete is enabled for this channel
	var settings ChannelSettings
	if err := db.Where("channel_id = ?", m.ChannelID).First(&settings).Error; err != nil {
		log.Printf("Error getting channel settings: %v", err)
		return
	}

	if !settings.Enabled {
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
