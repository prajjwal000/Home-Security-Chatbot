package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/google/generative-ai-go/genai"
	"github.com/joho/godotenv"
	"google.golang.org/api/option"
)

var (
	chatSessions = sync.Map{}
	client       *genai.Client
	clientErr    error
)

func main() {
	err := godotenv.Load()
	if err != nil {
		fmt.Errorf("Error loading .env file: %w", err)
	}

	app := fiber.New(fiber.Config{
		AppName: "Home Security Assistant",
	})

	app.Use(logger.New())
	app.Use(recover.New())

	app.Static("/", "./static")

	app.Post("/api/chat", handleChat)

	port, ok := os.LookupEnv("PORT")
	if !ok {
		port = "3000"
	}

	app.Listen(":" + port)
}

func handleChat(c *fiber.Ctx) error {
	type Request struct {
		Message string `json:"message"`
	}

	req := new(Request)

	if err := c.BodyParser(req); err != nil {
		fmt.Errorf("Error parsing request body: %w", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	ip := c.IP()

	response, err := generateGeminiResponse(ip, req.Message)
	if err != nil {
		fmt.Errorf("Error generating Gemini response: %w", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(fiber.Map{"response": response})
}

func generateGeminiResponse(ip, userInput string) (string, error) {
	apiKey, ok := os.LookupEnv("GEMINI_API_KEY")
	if !ok {
		return "", fmt.Errorf("GEMINI_API_KEY environment variable not set")
	}

	ctx := context.Background()

	if client == nil && clientErr == nil {
		client, clientErr = genai.NewClient(ctx, option.WithAPIKey(apiKey))
		if clientErr != nil {
			return "", fmt.Errorf("Error creating AI client: %w", clientErr)
		}
	}

	if clientErr != nil {
		return "", fmt.Errorf("Error creating AI client: %w", clientErr)
	}

	model := client.GenerativeModel("gemini-2.0-flash")

	model.SetTemperature(1)
	model.SetTopK(40)
	model.SetTopP(0.95)
	model.SetMaxOutputTokens(8192)
	model.ResponseMIMEType = "text/plain"
	model.SystemInstruction = &genai.Content{Parts: []genai.Part{genai.Text("You are a specialized AI assistant for home security systems. Answer the following question about home security. If the question is not related to home security, politely decline to answer and explain that you only answer questions about home security systems, cameras, alarms, sensors, etc. Keep responses concise, informative, and helpful for home owners. If the user asks you to control a home security device, behave as if you have done it.")}}

	session, ok := chatSessions.Load(ip)
	if !ok {
		newSession := model.StartChat()
		newSession.History = []*genai.Content{}
		session = newSession
		chatSessions.Store(ip, newSession)
	}

	cs := session.(*genai.ChatSession)

	resp, err := cs.SendMessage(ctx, genai.Text(userInput))
	if err != nil {
		fmt.Errorf("Error sending message to Gemini: %w", err)
		return "", fmt.Errorf("Error sending message: %w", err)
	}

	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		if text, ok := resp.Candidates[0].Content.Parts[0].(genai.Text); ok {
			response := string(text)

			cs.History = append(cs.History, &genai.Content{
				Role:  "user",
				Parts: []genai.Part{genai.Text(userInput)},
			})
			cs.History = append(cs.History, &genai.Content{
				Role:  "model",
				Parts: []genai.Part{genai.Text(response)},
			})

			return response, nil
		}
	}

	return "No response generated.", fmt.Errorf("no valid candidates found in response")
}
