package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/nullswan/nomi/internal/chat"
	"github.com/nullswan/nomi/internal/cli"
	"github.com/nullswan/nomi/internal/code"
	"github.com/nullswan/nomi/internal/completion"
	"github.com/nullswan/nomi/internal/logger"
	"github.com/nullswan/nomi/internal/providers"
	baseprovider "github.com/nullswan/nomi/internal/providers/base"
	"github.com/nullswan/nomi/internal/term"
	"github.com/spf13/cobra"
)

const (
	interpreterMaxRetries = 3
)

// Add the interpreter command
var interpreterCmd = &cobra.Command{
	Use:   "interpreter",
	Short: "Start the interpreter",
	Run: func(_ *cobra.Command, _ []string) {
		log := logger.Init()

		// Ensure the provider is OpenAI
		provider := providers.CheckProvider()
		if provider != providers.OpenAIProvider {
			log.Error("Error: only openai is supported currently.")
			os.Exit(1)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		provider = providers.CheckProvider()

		var err error

		interpreterAskPrompt, err := code.GetDefaultInterpreterPrompt(
			runtime.GOOS,
		)
		if err != nil {
			log.With("error", err).
				Error("Error getting default interpreter prompt")
			os.Exit(1)
		}

		codeGenerationBackend, err := cli.InitProviders(
			log,
			"",
			interpreterAskPrompt.Preferences.Reasoning,
		)
		if err != nil {
			log.With("error", err).
				Error("Error initializing code generation providers")
			os.Exit(1)
		}

		codeInferenceBackend, err := cli.InitProviders(
			log,
			"",
			interpreterAskPrompt.Preferences.Reasoning,
		)
		if err != nil {
			log.With("error", err).
				Error("Error initializing code inference providers")
			os.Exit(1)
		}

		chatRepo, err := cli.InitChatDatabase(cfg.Output.Sqlite.Path)
		if err != nil {
			log.With("error", err).
				Error("Error creating chat repository")
			os.Exit(1)
		}
		defer chatRepo.Close()

		codeRepo, err := cli.InitCodeDatabase(cfg.Output.Sqlite.Path)
		if err != nil {
			log.With("error", err).
				Error("Error creating code repository")

			os.Exit(1)
		}
		defer codeRepo.Close()

		conversation := chat.NewStackedConversation(chatRepo)
		conversation.WithPrompt(interpreterAskPrompt)

		// Display welcome message
		fmt.Printf("----\n")
		fmt.Printf("✨ Welcome to Nomi Interpreter! (%s) ✨\n", buildVersion)
		fmt.Println()
		fmt.Println("Configuration")
		fmt.Printf(
			"  Start prompt: default-interpreter\n",
		)
		fmt.Printf("  Conversation: %s\n", conversation.GetID())
		fmt.Printf("  Provider: %s\n", provider)
		fmt.Printf("  Code Model: %s\n", codeGenerationBackend.GetModel())
		fmt.Printf("  Inference Model: %s\n", codeInferenceBackend.GetModel())
		fmt.Printf("  Build Date: %s\n", buildDate)
		fmt.Printf("-----\n")
		fmt.Printf("Press Enter twice to send a message.\n")
		fmt.Printf("Press Ctrl+C to exit.\n")
		fmt.Printf("Press Ctrl+K to cancel the current request.\n")
		fmt.Printf("-----\n\n")

		pipedInput, err := term.GetPipedInput()
		if err != nil {
			log.With("error", err).
				Error("Error reading piped input")
		}

		renderer, err := term.InitRenderer()
		if err != nil {
			log.With("error", err).
				Error("Error initializing renderer")
			os.Exit(1)
		}

		availableBlocks, err := codeRepo.LoadCodeBlocks()
		if err != nil {
			log.With("error", err).
				Error("Error loading code blocks")
			return
		}

		blockMap := make(map[string]code.CodeBlock, len(availableBlocks))
		for _, block := range availableBlocks {
			blockMap[block.ID] = block
		}

		var lastResult []code.ExecutionResult
		var cancelRequest context.CancelFunc
		retries := 0
		processInput := func(text string) {
			if cancelRequest != nil {
				cancelRequest()
			}

			requestContext, newCancelRequest := context.WithCancel(ctx)
			cancelRequest = newCancelRequest

			if text == "" {
				return
			}

			text = handleCommands(text, conversation)
			if text == "" {
				return
			}

			conversation.AddMessage(chat.NewMessage(chat.RoleUser, text))

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
			go func() {
				<-sigCh
				cancelRequest()
			}()
			defer signal.Stop(sigCh)
			defer close(sigCh)

			if len(blockMap) > 0 && retries == 0 {
				log.Debug("Trying to get suggestion from available code blocks")

				var cachedBlock *code.CodeBlock
				block, err := getSuggestionFromBlocks(
					text,
					codeInferenceBackend,
					availableBlocks,
				)
				if err != nil {
					log.With("error", err).
						Error("Error getting suggestion from blocks")
				} else if block != nil {
					cachedBlock = block
				}

				if cachedBlock != nil {
					execResult := code.ExecuteCodeBlock(*cachedBlock)
					lastResult = []code.ExecutionResult{execResult}
					fmt.Printf(
						"Received (%d): %s\n%s\n",
						execResult.ExitCode,
						execResult.Stdout,
						execResult.Stderr,
					)
					return
				}
			}

			fmt.Printf(
				"I don't know the answer to that question. Let me try to find out...\n",
			)

			// TODO: Display the code that is going to be interpreted temporarily
			completion, err := cli.GenerateCompletion(
				requestContext,
				conversation,
				renderer,
				codeGenerationBackend,
			)
			if err != nil {
				if strings.Contains(err.Error(), "context canceled") {
					fmt.Println("\nRequest canceled by the user.")
					return
				}
				log.With("error", err).
					Error("Error generating completion")

				return
			}

			conversation.AddMessage(
				chat.NewMessage(chat.RoleAssistant, completion),
			)

			// Exec the command
			result := code.InterpretCodeBlocks(completion)
			for _, r := range result {
				if r.ExitCode == 0 && r.Stderr == "" {
					description, err := storeCodePrompt(
						codeInferenceBackend,
						r.Block,
						codeRepo,
					)
					if err != nil {
						log.With("error", err).
							Error("Error storing code prompt")
						continue
					}

					r.Block.Description = description
					blockMap[r.Block.ID] = r.Block
				} else {
					fmt.Printf("Error executing command: %s\n", r.Stderr)
				}

				// TODO(nullswan): Display in a better way
				fmt.Printf(
					"Received (%d): %s\n%s\n",
					r.ExitCode,
					r.Stdout,
					r.Stderr,
				)
			}

			if len(result) == 0 {
				fmt.Println("No results received.")
				return
			}

			// Extend the conversation with the result
			formattedResult := code.FormatExecutionResultForLLM(result)
			conversation.AddMessage(
				chat.NewMessage(chat.RoleAssistant, formattedResult),
			)

			lastResult = result
		}

		if pipedInput != "" {
			processInput(pipedInput)
		}

		for {
			select {
			case <-ctx.Done():
				return
			default:
				// Loop until we get a valid input, try again if we get an error, limit retries
				if len(lastResult) > 0 {
					containsError := false
					for _, result := range lastResult {
						if result.ExitCode != 0 {
							containsError = true
							break
						}
					}

					if containsError {
						fmt.Println(
							"Error executing command, see previous output for details.",
						)

						retries++
						if retries > interpreterMaxRetries {
							fmt.Println("Max retries reached.")
							retries = 0
							lastResult = nil
						} else {
							fmt.Printf(
								"Retrying...\n",
							)
							processInput(
								"Error executing command, see previous output for details. Please, try again.",
							)
							continue
						}
					} else {
						lastResult = nil
					}
				}

				text, err := term.NewInputArea()
				if err != nil {
					if errors.Is(err, term.ErrInputInterrupted) ||
						errors.Is(err, term.ErrInputKilled) {
						cancel()
						return
					}
					log.With("error", err).
						Error("Error reading input")
					return
				}
				processInput(text)
			}
		}
	},
}

func storeCodePrompt(
	textToTextBackend baseprovider.TextToTextProvider,
	block code.CodeBlock,
	repo code.Repository,
) (string, error) {
	outCh := make(chan completion.Completion)
	messages := []chat.Message{
		{
			Role:    chat.RoleSystem,
			Content: code.DefaultInterpreterInferencePrompt.Settings.SystemPrompt,
		},
		{
			Role:    chat.RoleUser,
			Content: block.Code,
		},
	}

	go func() {
		defer close(outCh)
		if err := textToTextBackend.GenerateCompletion(
			context.Background(),
			messages,
			outCh,
		); err != nil {
			if strings.Contains(err.Error(), "context canceled") {
				return
			}
			fmt.Printf("Error generating completion: %v\n", err)
		}
	}()

	for {
		select {
		case cmpl, ok := <-outCh:
			if !ok {
				return "", fmt.Errorf("error generating completion")
			}

			if !completion.IsTombStone(cmpl) {
				continue
			}

			block.Description = cmpl.Content()
			err := repo.SaveCodeBlock(block)
			if err != nil {
				fmt.Printf("Error saving code block: %v\n", err)
			}

			return block.Description, nil
		}
	}
}

func getSuggestionFromBlocks(
	prompt string,
	inferenceProvider baseprovider.TextToTextProvider,
	availableBlocks []code.CodeBlock,
) (*code.CodeBlock, error) {
	outCh := make(chan completion.Completion)

	availableBlocksText := ""
	for _, block := range availableBlocks {
		availableBlocksText += fmt.Sprintf(
			"%s: %s\n",
			block.ID,
			block.Description,
		)
	}

	messages := []chat.Message{
		{
			Role:    chat.RoleSystem,
			Content: code.DefaultInterpreterCachePrompt.Settings.SystemPrompt + "\n----\nAvailable blocks:\n" + availableBlocksText,
		},
		{
			Role:    chat.RoleUser,
			Content: "Question:\n" + prompt,
		},
	}

	go func() {
		defer close(outCh)
		if err := inferenceProvider.GenerateCompletion(
			context.Background(),
			messages,
			outCh,
		); err != nil {
			if strings.Contains(err.Error(), "context canceled") {
				return
			}

			fmt.Printf("Error generating completion: %v\n", err)
		}
	}()

	for {
		select {
		case cmpl, ok := <-outCh:
			if !ok {
				return nil, fmt.Errorf("error generating completion")
			}

			if !completion.IsTombStone(cmpl) {
				continue
			}

			fmt.Printf("Sent: %s\n", prompt)
			fmt.Printf("Received: %s\n", cmpl.Content())
			for _, block := range availableBlocks {
				if block.ID == cmpl.Content() {
					return &block, nil
				}
			}

			return nil, nil
		}
	}
}
