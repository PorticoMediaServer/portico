package live

import "time"

// A live channel watched is a stream like any title (area 11 rule 22; area 7 rule 37): it is
// in the owner's Now playing and counts against the stream limits. These are what the rest of
// the server reads of the viewings for that.

// All is every viewing now, newest first is the caller's to sort.
func (v *Viewings) All() []Viewing {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]Viewing, 0, len(v.byID))
	for _, w := range v.byID {
		out = append(out, *w)
	}
	return out
}

// Others counts the viewings other than one sign-in's own (which a new start on that device
// replaces): the account's, and the server's.
func (v *Viewings) Others(userID, signInID int64) (account, total int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, w := range v.byID {
		if signInID != 0 && w.SignInID == signInID {
			continue
		}
		total++
		if w.UserID == userID {
			account++
		}
	}
	return account, total
}

type stoppedViewing struct {
	userID  int64
	message string
	at      time.Time
}

// StopFor ends any account's viewing on the owner's or an administrator's word, and keeps
// their message for a while, for the viewer's device to be told when it next asks. It says
// whether there was such a viewing.
func (v *Viewings) StopFor(id, message string) bool {
	v.mu.Lock()
	w := v.byID[id]
	if w != nil {
		v.stopped[id] = stoppedViewing{userID: w.UserID, message: message, at: v.now()}
	}
	v.mu.Unlock()
	if w == nil {
		return false
	}
	v.Stop(id, w.UserID)
	return true
}

// StoppedFor says that one of the account's viewings was stopped by the owner or an
// administrator a moment ago, with their message.
func (v *Viewings) StoppedFor(id string, userID int64) (message string, ok bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	st, ok := v.stopped[id]
	if !ok || st.userID != userID {
		return "", false
	}
	return st.message, true
}
