package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/notices"
)

// Notifications: what the server tells each person. Everyone has an inbox with Unread, All and
// Archived and a real unread count; there is no email and no push. Open apps hear of a change
// through the events feed (topic "notice") and read their inbox again.

// Notification is one notice. An app words it from Kind and Params; Body is text a person
// wrote (the owner's message, a reply to a report), shown as it is.
type Notification struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Params JSON   `json:"params,omitempty"`
	Body   string `json:"body,omitempty"`
	// Ref names what it is about: a report's id for a reply.
	Ref       string    `json:"ref,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	Read      bool      `json:"read"`
	Archived  bool      `json:"archived"`
}

// Notifications is a page of one view of the inbox, with the unread count.
type Notifications struct {
	Items       []Notification `json:"items"`
	NextCursor  string         `json:"nextCursor,omitempty"`
	UnreadCount int            `json:"unreadCount"`
}

// NotificationEdit marks a notice read or unread, archived or not. A field left out stays.
type NotificationEdit struct {
	Read     *bool `json:"read,omitempty"`
	Archived *bool `json:"archived,omitempty"`
}

// ServerMessage is a message from the owner to everyone, or a tool's notice to the owner and
// administrators. Audience is "everyone" or "admins".
type ServerMessage struct {
	Audience string `json:"audience"`
	Message  string `json:"message"`
}

// ServerMessageSent says how many accounts it went to.
type ServerMessageSent struct {
	ID   string `json:"id"`
	Sent int    `json:"sent"`
}

const noticesPage = 50

func (s *Server) notificationRoutes() {
	Handle(s, "GET /v1/notifications", WithProfile, func(r *http.Request, _ None) (Notifications, error) {
		view := r.URL.Query().Get("view")
		switch view {
		case "":
			view = db.NoticesUnread
		case db.NoticesUnread, db.NoticesAll, db.NoticesArchived:
		default:
			return Notifications{}, ErrField("view", "Choose unread, all or archived.")
		}
		after, err := cursorID(r)
		if err != nil {
			return Notifications{}, err
		}
		list, unread, err := s.db.Notices(r.Context(), viewer(r), view, after, noticesPage+1)
		if err != nil {
			return Notifications{}, err
		}
		out := Notifications{Items: []Notification{}, UnreadCount: unread}
		if len(list) > noticesPage {
			list = list[:noticesPage]
			out.NextCursor = strconv.FormatInt(list[len(list)-1].ID, 10)
		}
		for _, n := range list {
			item := Notification{ID: n.PublicID, Kind: n.Kind, Body: n.Body, Ref: n.Ref, CreatedAt: n.CreatedAt.UTC(),
				Read: n.ReadAt != nil, Archived: n.ArchivedAt != nil}
			if n.Params != "" {
				item.Params = JSON(n.Params)
			}
			out.Items = append(out.Items, item)
		}
		return out, nil
	})
	Handle(s, "PATCH /v1/notifications/{id}", WithProfile, func(r *http.Request, req NotificationEdit) (None, error) {
		err := s.db.MarkNotice(r.Context(), viewer(r), r.PathValue("id"), req.Read, req.Archived, s.now())
		if errors.Is(err, db.ErrNotFound) {
			return None{}, ErrNotFound()
		}
		return None{}, err
	})
	Handle(s, "POST /v1/notifications/read-all", WithProfile, func(r *http.Request, _ None) (None, error) {
		return None{}, s.db.ReadAllNotices(r.Context(), viewer(r), s.now())
	})
	Handle(s, "POST /v1/admin/notifications", Admin, s.sendServerMessage)
}

func (s *Server) sendServerMessage(r *http.Request, req ServerMessage) (ServerMessageSent, error) {
	c := CallerFrom(r.Context())
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" || utf8.RuneCountInString(req.Message) > 2000 {
		return ServerMessageSent{}, ErrField("message", "Write a message of up to 2,000 characters.")
	}
	n := db.NewNotice{Kind: "message", Body: req.Message, ActorID: c.UserID, RequestHash: requestHash(req), Now: s.now()}
	switch req.Audience {
	case "everyone":
		// Only the owner messages everyone.
		if c.Role != "owner" {
			return ServerMessageSent{}, ErrNotPermitted()
		}
		n.To = db.ToEveryone
	case "admins":
		n.To, n.Kind = db.ToOwnerAndAdmins, "tool_message"
	default:
		return ServerMessageSent{}, ErrField("audience", "Choose everyone or admins.")
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return ServerMessageSent{}, err
	}
	n.IdempotencyKey = key
	id, sent, err := s.db.SendNotice(r.Context(), n)
	if errors.Is(err, db.ErrKeyReused) {
		return ServerMessageSent{}, errKeyReused()
	}
	return ServerMessageSent{ID: id, Sent: sent}, err
}

// cursorID reads a list's cursor: the id of the last row of the page before, 0 for the first.
func cursorID(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, ErrField("cursor", "This is not a cursor this list gave.")
	}
	return n, nil
}

// notices sends notices for the server's own areas. See package notices.
func (s *Server) notices() *notices.Notices {
	return &notices.Notices{DB: s.db, Settings: s.settings, Now: s.now}
}
