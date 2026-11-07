package oasis

import (
	"encoding/json"
	"fmt"
	"strings"

	"mirofish/internal/llm"
)

// Agent es un SocialAgent de Twitter con perfil y plataforma.
type Agent struct {
	ID       int
	Profile  Profile
	Platform *Twitter
	LLM      *llm.Client
}

// EnvironmentText replica SocialEnvironment.to_text_prompt: pide refresh y
// describe seguidores, seguidos y los posts visibles.
func (a *Agent) EnvironmentText() (string, error) {
	refreshed, err := a.Platform.Refresh(a.ID)
	if err != nil {
		return "", err
	}
	followers, follows, err := a.Platform.Stats(a.ID)
	if err != nil {
		return "", err
	}

	postsText := "After refreshing, there are no existing posts."
	if ok, _ := refreshed["success"].(bool); ok {
		if posts, exists := refreshed["posts"]; exists {
			if b, err := json.MarshalIndent(posts, "", "  "); err == nil {
				postsText = "After refreshing, you see some posts " + string(b)
			}
		}
	}

	return fmt.Sprintf(
		"You have %d followers and you follow %d users.\n%s\n"+
			"Pick one action that best reflects your current inclination based on "+
			"your profile and the posts above. Do not limit your action to just liking posts.",
		followers, follows, postsText,
	), nil
}

func (a *Agent) systemPrompt() string {
	return fmt.Sprintf(
		"You are %s (@%s), a participant in a social media simulation.\n"+
			"Bio: %s\nPersona: %s\n\n"+
			"You act on a Twitter-like platform. Choose exactly ONE action per turn "+
			"from this list and reply with a single JSON object, no markdown:\n"+
			`{"action": "create_post", "args": {"content": "<text>"}}`+"\n"+
			`{"action": "like_post", "args": {"post_id": <id>}}`+"\n"+
			`{"action": "repost", "args": {"post_id": <id>}}`+"\n"+
			`{"action": "quote_post", "args": {"post_id": <id>, "quote_content": "<text>"}}`+"\n"+
			`{"action": "follow", "args": {"followee_id": <agent_id>}}`+"\n"+
			`{"action": "create_comment", "args": {"post_id": <id>, "content": "<text>"}}`+"\n"+
			`{"action": "do_nothing", "args": {}}`+"\n"+
			"Stay in character. Write content in the language of your persona.",
		a.Profile.Name, a.Profile.UserName, a.Profile.Bio, a.Profile.Persona,
	)
}

// PerformActionByLLM replica perform_action_by_llm: observa el entorno, pide
// una acción al modelo y la ejecuta.
func (a *Agent) PerformActionByLLM() ActionResult {
	if a.LLM == nil {
		return ActionResult{Success: false, Error: "agente sin cliente LLM"}
	}
	envText, err := a.EnvironmentText()
	if err != nil {
		return ActionResult{Success: false, Error: err.Error()}
	}

	obj, err := a.LLM.ChatJSON([]llm.Message{
		{Role: "system", Content: a.systemPrompt()},
		{Role: "user", Content: "Here is your social media environment:\n" + envText},
	}, 0.7, nil, 2)
	if err != nil {
		return ActionResult{Success: false, Error: err.Error()}
	}

	action, err := actionFromObject(obj)
	if err != nil {
		return ActionResult{Success: false, Error: err.Error()}
	}
	return a.PerformAction(action)
}

// actionFromObject convierte el objeto JSON del LLM en Action.
func actionFromObject(obj map[string]any) (Action, error) {
	raw, _ := obj["action"].(string)
	actionType := ActionType(strings.TrimSpace(raw))
	if !IsSupportedTwitterAction(actionType) {
		return Action{}, fmt.Errorf("acción no soportada: %q", raw)
	}
	args := map[string]any{}
	if rawArgs, ok := obj["args"].(map[string]any); ok {
		args = rawArgs
	}
	return Action{Type: actionType, Args: args}, nil
}

// PerformAction ejecuta una acción manual o del LLM con validación de args.
func (a *Agent) PerformAction(action Action) ActionResult {
	result := ActionResult{Action: action.Type, Args: action.Args, Success: true}
	args := action.Args
	if args == nil {
		args = map[string]any{}
	}

	fail := func(err error) ActionResult {
		result.Success = false
		result.Error = err.Error()
		return result
	}

	switch action.Type {
	case ActionCreatePost:
		content := stringArg(args, "content")
		data, err := a.Platform.CreatePost(a.ID, content)
		if err != nil {
			return fail(err)
		}
		result.Data = data
	case ActionLikePost:
		postID, ok := intArg(args, "post_id")
		if !ok || postID <= 0 {
			return fail(fmt.Errorf("like_post requiere post_id válido"))
		}
		data, err := a.Platform.LikePost(a.ID, int64(postID))
		if err != nil {
			return fail(err)
		}
		result.Data = data
	case ActionRepost:
		postID, ok := intArg(args, "post_id")
		if !ok || postID <= 0 {
			return fail(fmt.Errorf("repost requiere post_id válido"))
		}
		data, err := a.Platform.Repost(a.ID, int64(postID))
		if err != nil {
			return fail(err)
		}
		result.Data = data
	case ActionQuotePost:
		postID, ok := intArg(args, "post_id")
		if !ok || postID <= 0 {
			return fail(fmt.Errorf("quote_post requiere post_id válido"))
		}
		data, err := a.Platform.QuotePost(a.ID, int64(postID), stringArg(args, "quote_content"))
		if err != nil {
			return fail(err)
		}
		result.Data = data
	case ActionCreateComment:
		postID, ok := intArg(args, "post_id")
		if !ok || postID <= 0 {
			return fail(fmt.Errorf("create_comment requiere post_id válido"))
		}
		data, err := a.Platform.CreateComment(a.ID, int64(postID), stringArg(args, "content"))
		if err != nil {
			return fail(err)
		}
		result.Data = data
	case ActionFollow:
		followee, ok := intArg(args, "followee_id")
		if !ok || followee < 0 {
			return fail(fmt.Errorf("follow requiere followee_id válido"))
		}
		data, err := a.Platform.Follow(a.ID, followee)
		if err != nil {
			return fail(err)
		}
		result.Data = data
	case ActionDoNothing:
		result.Data = map[string]any{"success": true}
		if err := a.Platform.TraceAction(a.ID, ActionDoNothing, args); err != nil {
			return fail(err)
		}
	default:
		return fail(fmt.Errorf("acción no soportada en esta rebanada: %s", action.Type))
	}
	return result
}
