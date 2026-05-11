package api

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/godeps/aigo"

	"github.com/cinience/saker/pkg/config"
	"github.com/cinience/saker/pkg/media/transcribe"
	"github.com/cinience/saker/pkg/model"
	"github.com/cinience/saker/pkg/runtime/commands"
	"github.com/cinience/saker/pkg/runtime/skills"
	"github.com/cinience/saker/pkg/runtime/tasks"
	sandboxenv "github.com/cinience/saker/pkg/sandbox/env"
	"github.com/cinience/saker/pkg/security"
	"github.com/cinience/saker/pkg/tool"
	toolbuiltin "github.com/cinience/saker/pkg/tool/builtin"
	aigotools "github.com/cinience/saker/pkg/tool/builtin/aigo"
)

func builtinToolFactories(root string, sandboxDisabled bool, entry EntryPoint, settings *config.Settings, skReg *skills.Registry, cmdExec *commands.Executor, taskStore tasks.Store, mdl model.Model, contextWindowTokens int, aigoCfg *config.AigoConfig, canvasDir string, execEnvOpt ...sandboxenv.ExecutionEnvironment) map[string]func() tool.Tool {
	factories := map[string]func() tool.Tool{}
	var execEnv sandboxenv.ExecutionEnvironment
	if len(execEnvOpt) > 0 {
		execEnv = execEnvOpt[0]
	}

	var (
		syncThresholdBytes  int
		asyncThresholdBytes int
	)
	if settings != nil && settings.BashOutput != nil {
		if settings.BashOutput.SyncThresholdBytes != nil {
			syncThresholdBytes = *settings.BashOutput.SyncThresholdBytes
		}
		if settings.BashOutput.AsyncThresholdBytes != nil {
			asyncThresholdBytes = *settings.BashOutput.AsyncThresholdBytes
		}
	}
	if asyncThresholdBytes > 0 {
		toolbuiltin.DefaultAsyncTaskManager().SetMaxOutputLen(asyncThresholdBytes)
	}

	bashCtor := func() tool.Tool {
		var bash *toolbuiltin.BashTool
		if sandboxDisabled {
			bash = toolbuiltin.NewBashToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			bash = toolbuiltin.NewBashToolWithRoot(root)
		}
		if syncThresholdBytes > 0 {
			bash.SetOutputThresholdBytes(syncThresholdBytes)
		}
		bash.SetEnvironment(execEnv)
		if entry == EntryPointCLI {
			bash.AllowShellMetachars(true)
		}
		return bash
	}

	readCtor := func() tool.Tool {
		var read *toolbuiltin.ReadTool
		if sandboxDisabled {
			read = toolbuiltin.NewReadToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			read = toolbuiltin.NewReadToolWithRoot(root)
		}
		read.SetEnvironment(execEnv)
		return read
	}
	writeCtor := func() tool.Tool {
		var write *toolbuiltin.WriteTool
		if sandboxDisabled {
			write = toolbuiltin.NewWriteToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			write = toolbuiltin.NewWriteToolWithRoot(root)
		}
		write.SetEnvironment(execEnv)
		return write
	}
	editCtor := func() tool.Tool {
		var edit *toolbuiltin.EditTool
		if sandboxDisabled {
			edit = toolbuiltin.NewEditToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			edit = toolbuiltin.NewEditToolWithRoot(root)
		}
		edit.SetEnvironment(execEnv)
		return edit
	}

	respectGitignore := true
	if settings != nil && settings.RespectGitignore != nil {
		respectGitignore = *settings.RespectGitignore
	}
	grepCtor := func() tool.Tool {
		var grep *toolbuiltin.GrepTool
		if sandboxDisabled {
			grep = toolbuiltin.NewGrepToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			grep = toolbuiltin.NewGrepToolWithRoot(root)
		}
		grep.SetEnvironment(execEnv)
		grep.SetRespectGitignore(respectGitignore)
		return grep
	}
	globCtor := func() tool.Tool {
		var glob *toolbuiltin.GlobTool
		if sandboxDisabled {
			glob = toolbuiltin.NewGlobToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			glob = toolbuiltin.NewGlobToolWithRoot(root)
		}
		glob.SetEnvironment(execEnv)
		glob.SetRespectGitignore(respectGitignore)
		return glob
	}
	// Keep a defensive fallback because this helper is called directly in tests
	// and package-internal wiring paths outside Runtime.New.
	if taskStore == nil {
		taskStore = tasks.NewTaskStore()
	}

	factories["bash"] = bashCtor
	factories["file_read"] = readCtor
	factories["image_read"] = func() tool.Tool {
		if sandboxDisabled {
			return toolbuiltin.NewImageReadToolWithSandbox(root, security.NewDisabledSandbox())
		}
		return toolbuiltin.NewImageReadToolWithRoot(root)
	}
	factories["canvas_get_node"] = func() tool.Tool {
		return toolbuiltin.NewCanvasGetNodeTool(canvasDir)
	}
	factories["canvas_list_nodes"] = func() tool.Tool {
		return toolbuiltin.NewCanvasListNodesTool(canvasDir)
	}
	factories["canvas_table_write"] = func() tool.Tool {
		return toolbuiltin.NewCanvasTableWriteTool(canvasDir)
	}
	factories["file_write"] = writeCtor
	factories["file_edit"] = editCtor
	factories["grep"] = grepCtor
	factories["glob"] = globCtor
	factories["web_fetch"] = func() tool.Tool { return toolbuiltin.NewWebFetchTool(nil) }
	factories["web_search"] = func() tool.Tool { return toolbuiltin.NewWebSearchTool(nil) }
	factories["bash_output"] = func() tool.Tool { return toolbuiltin.NewBashOutputTool(nil) }
	factories["bash_status"] = func() tool.Tool { return toolbuiltin.NewBashStatusTool() }
	factories["kill_task"] = func() tool.Tool { return toolbuiltin.NewKillTaskTool() }
	factories["task_create"] = func() tool.Tool { return toolbuiltin.NewTaskCreateTool(taskStore) }
	factories["task_list"] = func() tool.Tool { return toolbuiltin.NewTaskListTool(taskStore) }
	factories["task_get"] = func() tool.Tool { return toolbuiltin.NewTaskGetTool(taskStore) }
	factories["task_update"] = func() tool.Tool { return toolbuiltin.NewTaskUpdateTool(taskStore) }
	factories["ask_user_question"] = func() tool.Tool { return toolbuiltin.NewAskUserQuestionTool() }
	factories["skill"] = func() tool.Tool {
		st := toolbuiltin.NewSkillTool(skReg, nil)
		st.SetContextWindow(resolveContextWindow(contextWindowTokens, mdl))
		return st
	}
	factories["slash_command"] = func() tool.Tool { return toolbuiltin.NewSlashCommandTool(cmdExec) }
	factories["video_sampler"] = func() tool.Tool { return toolbuiltin.NewVideoSamplerTool() }
	factories["stream_capture"] = func() tool.Tool { return toolbuiltin.NewStreamCaptureTool() }
	factories["browser"] = func() tool.Tool { return toolbuiltin.NewBrowserTool() }
	factories["stream_monitor"] = func() tool.Tool { return toolbuiltin.NewStreamMonitorTool(taskStore) }
	factories["webhook"] = func() tool.Tool { return toolbuiltin.NewWebhookTool() }
	factories["media_index"] = func() tool.Tool {
		return toolbuiltin.NewMediaIndexTool(func(t *toolbuiltin.MediaIndexTool) {
			t.Model = mdl
		})
	}
	factories["media_search"] = func() tool.Tool { return toolbuiltin.NewMediaSearchTool() }
	if mdl != nil {
		factories["frame_analyzer"] = func() tool.Tool { return toolbuiltin.NewFrameAnalyzerTool(mdl) }
		factories["video_summarizer"] = func() tool.Tool { return toolbuiltin.NewVideoSummarizerTool(mdl) }
		factories["analyze_video"] = func() tool.Tool {
			t := toolbuiltin.NewAnalyzeVideoTool(mdl)
			// Inject TranscribeFunc from aigo ASR if available.
			if transcribeFn := resolveTranscribeFunc(aigoCfg); transcribeFn != nil {
				t.Transcribe = transcribeFn
			}
			// Set base store directory under project root (session subdirs resolved at runtime).
			if root != "" {
				t.StoreDir = filepath.Join(root, ".saker", "media")
			}
			return t
		}
	}

	if shouldRegisterTaskTool(entry) {
		factories["task"] = func() tool.Tool { return toolbuiltin.NewTaskTool() }
	}

	return factories
}

// resolveContextWindow determines the model's context window size (tokens).
// Priority: explicit value > dynamic interface > static registry > 0 (default budget).
func resolveContextWindow(explicit int, mdl model.Model) int {
	if explicit > 0 {
		return explicit
	}
	if mdl == nil {
		return 0
	}
	if cwp, ok := mdl.(model.ContextWindowProvider); ok {
		if tokens := cwp.ContextWindow(); tokens > 0 {
			return tokens
		}
	}
	if namer, ok := mdl.(model.ModelNamer); ok {
		if tokens := model.LookupContextWindow(namer.ModelName()); tokens > 0 {
			return tokens
		}
	}
	return 0
}

// resolveTranscribeFunc builds a TranscribeFunc for audio transcription.
// Priority: whisper CLI (local, no API cost) > aigo ASR (cloud-based).
// Returns nil if neither is available.
func resolveTranscribeFunc(aigoCfg *config.AigoConfig) toolbuiltin.TranscribeFunc {
	// Try whisper CLI first (faster, no API cost).
	if transcribe.WhisperAvailable() != "" {
		return transcribe.WhisperTranscribe
	}

	// Try aigo ASR.
	if aigoCfg == nil {
		return nil
	}
	asrEngines := aigoCfg.Routing["asr"]
	if len(asrEngines) == 0 {
		return nil
	}

	// Build a dedicated client with only ASR engines registered.
	client := sdk.NewClient()
	registered := false
	for _, ref := range asrEngines {
		providerName, modelName, err := aigotools.ParseRef(ref)
		if err != nil {
			slog.Warn("[aigo-asr] invalid routing ref", "ref", ref, "error", err)
			continue
		}
		provider, ok := aigoCfg.Providers[providerName]
		if !ok {
			continue
		}
		eng, err := aigotools.BuildEngine(provider, modelName, "asr")
		if err != nil {
			slog.Warn("[aigo-asr] build engine failed", "ref", ref, "error", err)
			continue
		}
		if err := client.RegisterEngine(ref, eng); err != nil {
			slog.Warn("[aigo-asr] register engine failed", "ref", ref, "error", err)
			continue
		}
		registered = true
	}
	if !registered {
		return nil
	}

	slog.Info("[aigo-asr] ASR transcription available", "engines", asrEngines)

	return func(ctx context.Context, audioPath string) (string, error) {
		// Convert local file to base64 data URI.
		data, err := os.ReadFile(audioPath)
		if err != nil {
			return "", fmt.Errorf("aigo-asr: read audio %s: %w", audioPath, err)
		}
		mimeType := mime.TypeByExtension(filepath.Ext(audioPath))
		if mimeType == "" {
			mimeType = "audio/wav"
		}
		dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))

		task := sdk.AgentTask{Prompt: dataURI}

		// Try engines with fallback.
		var lastErr error
		for _, eng := range asrEngines {
			result, err := client.ExecuteTask(ctx, eng, task)
			if err != nil {
				lastErr = err
				slog.Warn("[aigo-asr] engine failed", "engine", eng, "error", err)
				continue
			}
			text := strings.TrimSpace(result.Value)
			if text != "" {
				slog.Info("[aigo-asr] transcribed audio", "file", filepath.Base(audioPath), "engine", eng, "chars", len(text))
				return text, nil
			}
		}
		if lastErr != nil {
			return "", fmt.Errorf("aigo-asr: all engines failed: %w", lastErr)
		}
		return "", nil
	}
}
