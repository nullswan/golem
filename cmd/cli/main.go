package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/chzyer/readline"
	"github.com/gordonklaus/portaudio"
	"github.com/nullswan/nomi/internal/audio"
	"github.com/nullswan/nomi/internal/chat"
	"github.com/nullswan/nomi/internal/cli"
	"github.com/nullswan/nomi/internal/config"
	"github.com/nullswan/nomi/internal/logger"
	prompts "github.com/nullswan/nomi/internal/prompt"
	"github.com/nullswan/nomi/internal/providers"
	baseprovider "github.com/nullswan/nomi/internal/providers/base"
	"github.com/nullswan/nomi/internal/term"
	"github.com/nullswan/nomi/internal/transcription"
	hook "github.com/robotn/gohook"

	"github.com/spf13/cobra"
)

var (
	cfg                 *config.Config
	startPrompt         string
	interactiveMode     bool
	startConversationID string
	targetModel         string
)

const (
	binName = "nomi"
	// TODO(nullswan): Should be configurable
	cmdKeyCode = 55
)

func main() {
	// #region Config commands
	// rootCmd.AddCommand(configCmd)
	// configCmd.AddCommand(configShowCmd)
	// configCmd.AddCommand(configSetCmd)
	// configCmd.AddCommand(configSetupCmd)
	// #endregion

	// #region Interpreter commands
	rootCmd.AddCommand(interpreterCmd)
	// #endregion

	// #region Conversation commands
	rootCmd.AddCommand(conversationCmd)
	conversationCmd.AddCommand(conversationListCmd)
	conversationCmd.AddCommand(conversationShowCmd)
	conversationCmd.AddCommand(conversationDeleteCmd)
	// #endregion

	// #region Version commands
	rootCmd.AddCommand(versionCmd)
	// #endregion

	// #region Update commands
	rootCmd.AddCommand(updateCmd)
	// #endregion

	// #region Prompt commands
	rootCmd.AddCommand(promptCmd)
	promptCmd.AddCommand(promptListCmd)
	promptCmd.AddCommand(promptAddCmd)
	promptCmd.AddCommand(promptEditCmd)
	// #endregion

	// Attach flags to rootCmd only, so they are not inherited by subcommands
	rootCmd.Flags().
		StringVarP(&startPrompt, "prompt", "p", "", "Specify a prompt")
	rootCmd.Flags().
		StringVarP(&targetModel, "model", "m", "", "Specify a model")
	rootCmd.Flags().
		StringVarP(&startConversationID, "conversation", "c", "", "Open a conversation by ID")
	rootCmd.Flags().
		BoolVarP(&interactiveMode, "interactive", "i", false, "Start in interactive mode")

	// Initialize cfg in PersistentPreRun, making it available to all commands
	rootCmd.PersistentPreRun = func(_ *cobra.Command, _ []string) {
		if !config.Exists() {
			fmt.Println("Looks like this is your first time running nomi!")
			if err := config.Setup(); err != nil {
				fmt.Printf("Error during configuration setup: %v\n", err)
				os.Exit(1)
			}
		}

		var err error
		cfg, err = config.LoadConfig()
		if err != nil {
			fmt.Printf("Error loading config: %v\n", err)
			os.Exit(1)
		}

		// pprof
		// TODO(nullswan): Make optional
		go func() {
			if err := http.ListenAndServe("localhost:6060", nil); err != nil {
				fmt.Printf("Error starting pprof server: %v\n", err)
				os.Exit(1)
			}
		}()
	}

	// Execute the root command
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   binName + " [flags] [arguments]",
	Short: "An enhanced AI runtime, focusing on ease of use and extensibility.",
	Run:   runApp,
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

func runApp(_ *cobra.Command, _ []string) {
	// Setup context and signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("Sig received, quitting...")
		cancel()
	}()

	// Initialize Logger
	logger := logger.Init()

	selectedPrompt := &prompts.DefaultPrompt
	if startPrompt != "" {
		var err error
		selectedPrompt, err = prompts.LoadPrompt(startPrompt)
		if err != nil {
			fmt.Printf("Error loading prompt: %v\n", err)
			os.Exit(1)
		}
	}

	// Initialize Providers
	textToTextBackend, err := cli.InitProviders(
		logger,
		targetModel,
		selectedPrompt.Preferences.Reasoning,
	)
	if err != nil {
		fmt.Printf("Error initializing providers: %v\n", err)
		os.Exit(1)
	}
	defer textToTextBackend.Close()

	// Initialize Database
	repo, err := cli.InitChatDatabase(
		cfg.Output.Sqlite.Path,
	)
	if err != nil {
		fmt.Printf("Error creating repository: %v\n", err)
		os.Exit(1)
	}
	defer repo.Close()

	// Initialize Repository and Conversation
	conversation, err := initConversation(repo)
	if err != nil {
		fmt.Printf("Error initializing conversation: %v\n", err)
		os.Exit(1)
	}

	// Display Welcome Message
	if !interactiveMode {
		displayWelcome(conversation, textToTextBackend.GetModel())
	}

	// Initialize Renderer
	renderer, err := term.InitRenderer()
	if err != nil {
		fmt.Printf("Error initializing renderer: %v\n", err)
		os.Exit(1)
	}

	// Initialize Audio
	oaiKey := os.Getenv("OPENAI_API_KEY")
	if oaiKey == "" {
		fmt.Println("OPENAI_API_KEY is not set")
		os.Exit(1)
	}

	if err := portaudio.Initialize(); err != nil {
		fmt.Printf("Failed to initialize PortAudio: %v\n", err)
		os.Exit(1)
	}
	defer portaudio.Terminate()

	audioOpts, err := audio.ComputeAudioOptions(&audio.AudioOptions{})
	if err != nil {
		fmt.Printf("Error computing audio options: %v\n", err)
		os.Exit(1)
	}

	// Initialize Readline
	rl, err := term.InitReadline()
	if err != nil {
		fmt.Printf("Error initializing readline: %v\n", err)
		os.Exit(1)
	}
	defer rl.Close()

	inputCh := make(chan string)
	inputErrCh := make(chan error)

	// Initialize Transcription Server
	ts := initTranscriptionServer(
		oaiKey,
		audioOpts,
		logger,
		func(text string, isProcessing bool) {
			rl.Operation.Clean()
			if !isProcessing {
				rl.Operation.SetBuffer("")
				fmt.Printf("%s\n\n", text)
				inputCh <- text
			} else {
				rl.Operation.SetBuffer(text)
			}
		},
	)
	defer ts.Close()
	ts.Start()

	// Initialize VAD
	vad := initVAD(ts, logger)
	defer vad.Stop()
	vad.Start()

	// Create Input Stream
	inputStream, err := audio.NewInputStream(
		logger,
		audioOpts,
		func(buffer []float32) {
			vad.Feed(buffer)
		},
	)
	if err != nil {
		fmt.Printf("Failed to create input stream: %v\n", err)
		os.Exit(1)
	}

	defer inputStream.Close()

	// Start Input Reader Goroutine
	go readInput(rl, inputCh, inputErrCh)

	// Initialize Key Hooks
	audioStartCh, audioEndCh := setupKeyHooks()

	// Main Event Loop
	eventLoop(
		ctx,
		cancel,
		inputCh,
		inputErrCh,
		audioStartCh,
		audioEndCh,
		inputStream,
		logger,
		conversation,
		renderer,
		textToTextBackend,
		rl,
	)
}

func initConversation(repo chat.Repository) (chat.Conversation, error) {
	var err error
	var conversation chat.Conversation
	if startConversationID != "" {
		conversation, err = repo.LoadConversation(startConversationID)
		if err != nil {
			return nil, fmt.Errorf("error loading conversation: %w", err)
		}
	} else {
		conversation = chat.NewStackedConversation(repo)
		conversation.WithPrompt(prompts.DefaultPrompt)
	}

	return conversation, nil
}

func displayWelcome(conversation chat.Conversation, model string) {
	fmt.Printf("----\n")
	fmt.Printf("Nomi (%s)\n", buildVersion)
	fmt.Println()
	fmt.Println("Configuration")
	fmt.Printf("  Start prompt: %s\n", startPrompt)
	fmt.Printf("  Conversation: %s\n", conversation.GetID())
	fmt.Printf("  Provider: %s\n", providers.CheckProvider())
	fmt.Printf("  Model: %s\n", model)
	fmt.Printf("  Build Date: %s\n", buildDate)
	fmt.Printf("-----\n")
	fmt.Printf("Press Enter twice to send a message.\n")
	fmt.Printf("Press Ctrl+C to exit.\n")
	fmt.Printf("Press Ctrl+K to cancel the current request.\n")
	fmt.Printf("Press any[once] and CMD to record audio.\n")
	fmt.Printf("-----\n\n")
}

func initTranscriptionServer(
	oaiKey string,
	audioOpts *audio.AudioOptions,
	logger *logger.Logger,
	callback transcription.TranscriptionServerCallbackT,
) *transcription.TranscriptionServer {
	bufferManagerPrimary := transcription.NewBufferManager(audioOpts)
	bufferManagerPrimary.SetMinBufferDuration(500 * time.Millisecond)
	bufferManagerPrimary.SetOverlapDuration(100 * time.Millisecond)

	bufferManagerSecondary := transcription.NewBufferManager(audioOpts)
	bufferManagerSecondary.SetMinBufferDuration(2 * time.Second)
	bufferManagerSecondary.SetOverlapDuration(400 * time.Millisecond)

	textReconcilier := transcription.NewTextReconciler(logger)
	tsHandler := transcription.NewTranscriptionHandler(
		oaiKey,
		audioOpts,
		logger,
	)
	tsHandler.SetEnableFixing(true)

	ts := transcription.NewTranscriptionServer(
		bufferManagerPrimary,
		bufferManagerSecondary,
		tsHandler,
		textReconcilier,
		logger,
		callback,
	)
	return ts
}

func initVAD(
	ts *transcription.TranscriptionServer,
	logger *logger.Logger,
) *audio.VAD {
	vad := audio.NewVAD(
		audio.VADConfig{
			EnergyThreshold: 0.005,
			FlushInterval:   310 * time.Millisecond,
			SilenceDuration: 500 * time.Millisecond,
			PauseDuration:   300 * time.Millisecond,
		},
		audio.VADCallbacks{
			OnSpeechStart: func() {
				logger.Debug("VAD: Speech started")
			},
			OnSpeechEnd: func() {
				logger.Debug("VAD: Speech ended")
				ts.FlushBuffers()
			},
			OnFlush: func(buffer []float32) {
				logger.
					With("buf_sz", len(buffer)).
					Debug("VAD: Buffer flushed")

				data, err := audio.Float32ToPCM(buffer)
				if err != nil {
					logger.
						With("error", err).
						Error("Failed to convert float32 to PCM")
					return
				}

				ts.AddAudio(data)
			},
			OnPause: func() {
				logger.Debug("VAD: Speech paused")
				ts.FlushPrimaryBuffer()
			},
		},
		logger,
	)
	return vad
}

func readInput(
	rl *readline.Instance,
	inputCh chan<- string,
	inputErrCh chan<- error,
) {
	for {
		line, err := rl.Readline()
		if err != nil {
			fmt.Println("Error reading input", err)
			if err == readline.ErrInterrupt {
				inputErrCh <- term.ErrInputInterrupted
				return
			}
			if err == io.EOF {
				// when killed, wait for alive..
				inputErrCh <- term.ErrInputKilled
				return
			}
			inputErrCh <- fmt.Errorf("error reading input: %w", err)
			return
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		inputCh <- line
	}
}

func setupKeyHooks() (chan struct{}, chan struct{}) {
	audioStartCh := make(chan struct{}, 1)
	audioEndCh := make(chan struct{}, 1)

	// Key Code is not specified on purpose due to the way the hook library works
	hook.Register(hook.KeyHold, []string{""}, func(e hook.Event) {
		if e.Rawcode != cmdKeyCode {
			return
		}
		select {
		case audioStartCh <- struct{}{}:
		default:
		}
	})

	// Key Code is not specified on purpose due to the way the hook library works
	hook.Register(hook.KeyUp, []string{""}, func(e hook.Event) {
		if e.Rawcode != cmdKeyCode {
			return
		}

		select {
		case audioEndCh <- struct{}{}:
		default:
		}
	})

	s := hook.Start()
	go func() {
		<-hook.Process(s)
	}()
	return audioStartCh, audioEndCh
}

func eventLoop(
	ctx context.Context,
	cancel context.CancelFunc,
	inputCh chan string,
	inputErrCh chan error,
	audioStartCh, audioEndCh <-chan struct{},
	inputStream *audio.AudioStream,
	logger *logger.Logger,
	conversation chat.Conversation,
	renderer *term.Renderer,
	textToTextBackend baseprovider.TextToTextProvider,
	rl *readline.Instance,
) {
	audioRunning := false
	// var wg sync.WaitGroup

	defer func() {
		if audioRunning {
			inputStream.Stop()
		}
	}()

	eventCtx, eventCtxCancel := context.WithCancel(ctx)
	defer eventCtxCancel()

	for {
		select {
		case <-ctx.Done():
			return
		case line := <-inputCh:
			eventCtxCancel()
			eventCtx, eventCtxCancel = context.WithCancel(ctx)
			defer eventCtxCancel()

			processInput(
				eventCtx,
				line,
				conversation,
				renderer,
				textToTextBackend,
				rl,
			)
		case err := <-inputErrCh:
			if errors.Is(err, term.ErrInputInterrupted) ||
				errors.Is(err, term.ErrInputKilled) {
				cancel()
				break
			}
			fmt.Printf("Error reading input: %v\n", err)
		case <-audioStartCh:
			if !audioRunning {
				audioRunning = true
				// fmt.Println("Recording audio...")
				err := inputStream.Start()
				if err != nil {
					logger.
						With("error", err).
						Error("Failed to start input stream")
				}
				// closeReadline()
			}
		case <-audioEndCh:
			if audioRunning {
				audioRunning = false
				// fmt.Println("Audio recording stopped.")
				err := inputStream.Stop()
				if err != nil {
					logger.
						With("error", err).
						Error("Failed to stop input stream")
				}
				// reinitReadline(&wg, inputCh, inputErrCh)
			}
		}
	}
}

func setupReadline(
	rl *readline.Instance,
	inputCh chan<- string,
	inputErrCh chan<- error,
) {
	go readInput(rl, inputCh, inputErrCh)
}

func setupNewReadline() (*readline.Instance, error) {
	return term.InitReadline()
}

func processInput(
	ctx context.Context,
	text string,
	conversation chat.Conversation,
	renderer *term.Renderer,
	textToTextBackend baseprovider.TextToTextProvider,
	rl *readline.Instance,
) {
	defer rl.Refresh()

	if text == "" {
		return
	}

	text = handleCommands(text, conversation)
	if text == "" {
		return
	}

	conversation.AddMessage(chat.NewMessage(chat.RoleUser, text))

	completion, err := cli.GenerateCompletion(
		ctx,
		conversation,
		renderer,
		textToTextBackend,
	)
	if err != nil {
		if strings.Contains(err.Error(), "context canceled") {
			fmt.Println("\nRequest canceled by the user.")
			return
		}

		fmt.Printf("Error generating completion: %v\n", err)
		return
	}

	conversation.AddMessage(chat.NewMessage(chat.RoleAssistant, completion))
}

func handleCommands(text string, conversation chat.Conversation) string {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return text
	}

	ret := ""
	for _, line := range lines {
		if !strings.HasPrefix(line, "/") {
			ret += line + "\n"
			continue
		}

		switch {
		case strings.HasPrefix(line, "/help"):
			fmt.Println("Available commands:")
			fmt.Println("  /help        Show this help message")
			fmt.Println("  /reset       Reset the conversation")
			fmt.Println(
				"  /add <file>  Add a file or directory to the conversation",
			)
			fmt.Println("  /exit        Exit the application")
		case strings.HasPrefix(line, "/reset"):
			conversation = conversation.Reset()
			fmt.Println("Conversation reset.")
		case strings.HasPrefix(line, "/add"):
			args := strings.Fields(line)
			if len(args) < 2 {
				fmt.Println("Usage: /add <file or directory>")
				continue
			}

			if !isLocalResource(args[1]) {
				fmt.Println("Invalid file or directory: " + args[1])
				continue
			}
			processLocalResource(conversation, args[1])
		case strings.HasPrefix(line, "/exit"):
			fmt.Println("Exiting...")
			os.Exit(0)
		default:
			ret += line + "\n"
		}
	}

	return ret
}

func isLocalResource(text string) bool {
	var path string
	if strings.HasPrefix(text, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		path = filepath.Join(home, text[1:])
	} else {
		path = text
	}

	if strings.HasPrefix(path, "./") || strings.HasPrefix(path, "/") {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}

	return false
}

func processLocalResource(conversation chat.Conversation, text string) {
	if isDirectory(text) {
		addAllFiles(conversation, text)
	} else {
		addSingleFile(conversation, text)
	}
}

func isDirectory(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func addAllFiles(conversation chat.Conversation, directory string) {
	files, err := os.ReadDir(directory)
	if err != nil {
		fmt.Println("Error reading directory:", err)
		return
	}
	for _, file := range files {
		path := filepath.Join(directory, file.Name())
		if file.IsDir() {
			addAllFiles(conversation, path)
		} else {
			addFileToConversation(conversation, path, file.Name())
		}
	}
}

func addSingleFile(conversation chat.Conversation, filePath string) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Println("Error reading file:", err)
		return
	}
	fileName := filepath.Base(filePath)
	conversation.AddMessage(
		chat.NewFileMessage(
			chat.RoleUser,
			formatFileMessage(fileName, string(content)),
		),
	)
}

func addFileToConversation(
	conversation chat.Conversation,
	filePath, fileName string,
) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Println("Error reading file:", err)
		return
	}
	conversation.AddMessage(
		chat.NewFileMessage(
			chat.RoleUser,
			formatFileMessage(fileName, string(content)),
		),
	)
	fmt.Printf("Added file: %s\n", filePath)
}

func formatFileMessage(fileName, content string) string {
	return fileName + "-----\n" + content + "-----\n"
}
