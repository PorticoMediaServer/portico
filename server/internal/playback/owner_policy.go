package playback

import "errors"

var ErrOwnerAccountCap = errors.New("The owner's stream limit for this account has been reached.")
var ErrOwnerServerCap = errors.New("The owner's server stream limit has been reached.")
var ErrTranscodingDisabled = errors.New("This source requires conversion, which the server owner has disabled.")
