package handlers

import (
	"context"
	"fmt"
	"log"

	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func HandleAutoJoin(ctx context.Context, as *appservice.AppService, evt *event.Event) error {
	if evt.Type != event.StateMember {
		log.Printf("Auto-join: skip non-member event %s", evt.Type.String())
		return nil
	}
	if evt.StateKey == nil {
		log.Printf("Auto-join: missing state key for event %s", evt.ID.String())
		return nil
	}
	botMXID := as.BotMXID()
	if id.UserID(*evt.StateKey) != botMXID {
		log.Printf("Auto-join: state key %s does not match bot %s", *evt.StateKey, botMXID.String())
		return nil
	}
	if err := evt.Content.ParseRaw(evt.Type); err != nil {
		if err != event.ErrContentAlreadyParsed {
			log.Printf("Auto-join: failed to parse member content for %s: %v", evt.ID.String(), err)
			return nil
		}
	}
	memberContent, ok := evt.Content.Parsed.(*event.MemberEventContent)
	if !ok {
		log.Printf("Auto-join: unexpected content type for %s", evt.ID.String())
		return nil
	}
	if memberContent.Membership != event.MembershipInvite {
		log.Printf("Auto-join: membership is %s, not invite", memberContent.Membership)
		return nil
	}

	log.Printf("Auto-join: bot invited to room %s by %s", evt.RoomID.String(), evt.Sender.String())

	intent := as.BotIntent()
	if err := intent.EnsureJoined(ctx, evt.RoomID); err != nil {
		return fmt.Errorf("failed to auto-join room %s: %w", evt.RoomID.String(), err)
	}
	log.Printf("Auto-join: bot joined room %s", evt.RoomID.String())
	return nil
}
