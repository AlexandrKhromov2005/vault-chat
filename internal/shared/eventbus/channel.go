// Package eventbus names the Redis Pub/Sub channels through which Chat
// publishes events and Gateway relays them, so that neither service depends
// on the other's code.
package eventbus

import "strings"

// userChannelPrefix precedes the user ID in the name of a user's channel.
const userChannelPrefix = "chat:user:"

// UserChannel is the Pub/Sub channel carrying the events of one user.
func UserChannel(userID string) string { return userChannelPrefix + userID }

// UserFromChannel returns the user whose events a channel carries and whether
// it is a user channel at all.
func UserFromChannel(channel string) (string, bool) {
	user, ok := strings.CutPrefix(channel, userChannelPrefix)
	if !ok || user == "" {
		return "", false
	}
	return user, true
}
