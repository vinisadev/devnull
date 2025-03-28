package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type ChannelSettings struct {
	gorm.Model
	ChannelID          string `gorm:"uniqueIndex"`
	ServerID           string
	Enabled            bool
	DeleteAfterMinutes int `gorm:"default:2"`
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

	err = db.AutoMigrate(&ChannelSettings{})
	if err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}
}

func main() {
	initDB()

	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_BOT_TOKEN environment variable not set")
	}

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatalf("Error creating Discord session: %v", err)
	}

	dg.AddHandler(messageCreate)
	dg.AddHandler(messageCommand)

	err = dg.Open()
	if err != nil {
		log.Fatalf("Error opening connection: %v", err)
	}
	defer dg.Close()

	fmt.Println("Bot is now running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}

func messageCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if !strings.HasPrefix(m.Content, "!autodelete") {
		return
	}

	member, err := s.GuildMember(m.GuildID, m.Author.ID)
	if err != nil || !hasAdminPermissions(s, member) {
		return
	}

	parts := strings.Fields(m.Content)
	if len(parts) < 2 {
		s.ChannelMessageSend(m.ChannelID, "Usage: !autodelete [enable|disable|set] [minutes]")
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
		if len(parts) >= 3 {
			if minutes, err := strconv.Atoi(parts[2]); err == nil && minutes > 0 {
				settings.DeleteAfterMinutes = minutes
			}
		}
		msg := fmt.Sprintf("Auto-delete enabled for this channel (deleting after %d minutes)", settings.DeleteAfterMinutes)
		s.ChannelMessageSend(m.ChannelID, msg)
	case "disable":
		settings.Enabled = false
		s.ChannelMessageSend(m.ChannelID, "Auto-delete disabled for this channel")
	case "set":
		if len(parts) < 3 {
			s.ChannelMessageSend(m.ChannelID, "Usage: !autodelete set [minutes]")
			return
		}
		if minutes, err := strconv.Atoi(parts[2]); err == nil && minutes > 0 {
			settings.DeleteAfterMinutes = minutes
			msg := fmt.Sprintf("Auto-delete time updated to %d minutes", settings.DeleteAfterMinutes)
			s.ChannelMessageSend(m.ChannelID, msg)
		} else {
			s.ChannelMessageSend(m.ChannelID, "Invalid minutes value. Please provide a positive number.")
			return
		}
	default:
		s.ChannelMessageSend(m.ChannelID, "Usage: !autodelete [enable|disable|set] [minutes]")
		return
	}

	db.Save(&settings)
}

func hasAdminPermissions(s *discordgo.Session, member *discordgo.Member) bool {
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
	if m.Author.ID == s.State.User.ID || strings.HasPrefix(m.Content, "!") {
		return
	}

	var settings ChannelSettings
	if err := db.Where("channel_id = ?", m.ChannelID).First(&settings).Error; err != nil {
		log.Printf("Error getting channel settings: %v", err)
		return
	}

	if !settings.Enabled {
		return
	}

	go func() {
		deleteAfter := time.Duration(settings.DeleteAfterMinutes) * time.Minute
		time.Sleep(deleteAfter)
		err := s.ChannelMessageDelete(m.ChannelID, m.ID)
		if err != nil {
			log.Printf("Error deleting message: %v", err)
		}
	}()
}
