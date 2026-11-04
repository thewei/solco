package personalagent

import (
	"strconv"
)

// group serves `assistant group …`: the one Go-authoritative conversation API
// that the desktop, the phone's Host and the Group Agents all use.
//
//	create   {requestId,title?,members:[ids]}
//	list
//	show     <id> [--since N] [--limit N]
//	rename   <id> {requestId,title,expectedRevision}
//	add      <id> {requestId,assistantId}
//	remove   <id> {requestId,assistantId}
//	post     {requestId,groupId,body,mentions?,replyTo?}          the person speaks
//	say      {requestId,groupId,as,body,replyTo?}                 a member's Agent speaks
func (a *API) group(args []string, body []byte) (any, error) {
	if len(args) == 0 {
		return nil, coded("invalid", "usage: assistant group <create|list|show|rename|add|remove|post|say>")
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "list":
		list, err := a.Store.ListGroups()
		return ok("groups", list), err
	case "show":
		if len(rest) < 1 {
			return nil, coded("invalid", "usage: assistant group show <id> [--since N] [--limit N]")
		}
		id := rest[0]
		var since int64
		limit := 0
		if v, _ := flagValue(rest[1:], "--since"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return nil, coded("invalid", "--since must be a message seq")
			}
			since = n
		}
		if v, _ := flagValue(rest[1:], "--limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return nil, coded("invalid", "--limit must be a positive number")
			}
			limit = n
		}
		feed, err := a.Store.GroupFeedSince(id, since, limit)
		return ok("feed", feed), err
	case "create":
		var in struct {
			common
			Title   string   `json:"title"`
			Members []string `json:"members"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		g, replayed, err := a.Store.CreateGroup(in.RequestID, in.Title, in.Members)
		return ok("group", g, "replayed", replayed), err
	case "rename":
		var in struct {
			common
			Title string `json:"title"`
		}
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant group rename <id>")
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		g, replayed, err := a.Store.RenameGroup(in.RequestID, rest[0], in.Title, in.ExpectedRevision)
		return ok("group", g, "replayed", replayed), err
	case "add", "remove":
		var in struct {
			common
			AssistantID string `json:"assistantId"`
		}
		if len(rest) != 1 {
			return nil, coded("invalid", "usage: assistant group %s <id>", cmd)
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		var g Group
		var replayed bool
		var err error
		if cmd == "add" {
			g, replayed, err = a.Store.AddGroupMember(in.RequestID, rest[0], in.AssistantID)
		} else {
			g, replayed, err = a.Store.RemoveGroupMember(in.RequestID, rest[0], in.AssistantID)
		}
		return ok("group", g, "replayed", replayed), err
	case "post", "say":
		var in struct {
			common
			GroupID  string   `json:"groupId"`
			As       string   `json:"as"`
			Body     string   `json:"body"`
			Mentions []string `json:"mentions"`
			ReplyTo  string   `json:"replyTo"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		pin := PostMessageInput{GroupID: in.GroupID, Body: in.Body, Mentions: in.Mentions, ReplyTo: in.ReplyTo, AuthorKind: AuthorUser}
		if cmd == "say" {
			if in.As == "" {
				return nil, coded("invalid", "group say needs \"as\": the assistant that is speaking")
			}
			pin.AuthorKind, pin.AuthorID = AuthorAssistant, in.As
		} else if in.As != "" {
			return nil, coded("invalid", "group post is the person's own message; an assistant uses group say")
		}
		res, replayed, err := a.Store.PostGroupMessage(in.RequestID, pin)
		return ok("result", res, "replayed", replayed), err
	}
	return nil, coded("invalid", "unknown group command %q", cmd)
}
