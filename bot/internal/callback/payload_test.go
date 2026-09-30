package callback

import (
	"errors"
	"strings"
	"testing"

	"hackatonBotMAX/internal/domain"
)

func TestEncode(t *testing.T) {
	tests := []struct {
		name           string
		action         domain.ActionType
		registrationID string
		eventID        string
		want           string
		wantErr        error
	}{
		{
			name:           "confirm with event id",
			action:         domain.ActionConfirmRegistration,
			registrationID: "registration_15",
			eventID:        "event_42",
			want:           "v1|confirm|registration_15|event_42",
		},
		{
			name:           "cancel without event id",
			action:         domain.ActionCancelRegistration,
			registrationID: "registration_15",
			want:           "v1|cancel|registration_15",
		},
		{
			name:           "waitlist accept",
			action:         domain.ActionAcceptWaitlist,
			registrationID: "reg_1",
			eventID:        "ev_1",
			want:           "v1|wl_accept|reg_1|ev_1",
		},
		{
			name:           "waitlist decline",
			action:         domain.ActionDeclineWaitlist,
			registrationID: "reg_1",
			want:           "v1|wl_decline|reg_1",
		},
		{
			name:           "unknown action is rejected",
			action:         domain.ActionType("teleport"),
			registrationID: "reg_1",
			wantErr:        ErrUnknownAction,
		},
		{
			name:    "missing registration id is rejected",
			action:  domain.ActionConfirmRegistration,
			wantErr: ErrInvalidField,
		},
		{
			name:           "registration id containing the separator is rejected",
			action:         domain.ActionConfirmRegistration,
			registrationID: "reg|1",
			wantErr:        ErrInvalidField,
		},
		{
			name:           "event id containing the separator is rejected",
			action:         domain.ActionConfirmRegistration,
			registrationID: "reg_1",
			eventID:        "ev|1",
			wantErr:        ErrInvalidField,
		},
		{
			name:           "padded registration id is rejected",
			action:         domain.ActionConfirmRegistration,
			registrationID: " reg_1 ",
			wantErr:        ErrInvalidField,
		},
		{
			name:           "oversized payload is rejected",
			action:         domain.ActionConfirmRegistration,
			registrationID: strings.Repeat("x", MaxPayloadLen),
			wantErr:        ErrPayloadTooLong,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Encode(tc.action, tc.registrationID, tc.eventID)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Encode() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Encode() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Encode() = %q, want %q", got, tc.want)
			}
			if len(got) > MaxPayloadLen {
				t.Fatalf("Encode() produced %d bytes, over the MAX limit of %d", len(got), MaxPayloadLen)
			}
		})
	}
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Payload
		wantErr error
	}{
		{
			name: "confirm with event id",
			raw:  "v1|confirm|registration_15|event_42",
			want: Payload{
				Version:        "v1",
				Action:         domain.ActionConfirmRegistration,
				RegistrationID: "registration_15",
				EventID:        "event_42",
			},
		},
		{
			name: "cancel without event id",
			raw:  "v1|cancel|registration_15",
			want: Payload{
				Version:        "v1",
				Action:         domain.ActionCancelRegistration,
				RegistrationID: "registration_15",
			},
		},
		{
			name: "waitlist accept",
			raw:  "v1|wl_accept|reg_1|ev_1",
			want: Payload{
				Version:        "v1",
				Action:         domain.ActionAcceptWaitlist,
				RegistrationID: "reg_1",
				EventID:        "ev_1",
			},
		},
		{
			name: "waitlist decline",
			raw:  "v1|wl_decline|reg_1",
			want: Payload{
				Version:        "v1",
				Action:         domain.ActionDeclineWaitlist,
				RegistrationID: "reg_1",
			},
		},
		{
			name: "surrounding whitespace is tolerated",
			raw:  "  v1|confirm|reg_1  ",
			want: Payload{
				Version:        "v1",
				Action:         domain.ActionConfirmRegistration,
				RegistrationID: "reg_1",
			},
		},
		{name: "empty payload", raw: "", wantErr: ErrEmptyPayload},
		{name: "whitespace only", raw: "   ", wantErr: ErrEmptyPayload},
		{name: "future version", raw: "v2|confirm|reg_1", wantErr: ErrUnsupportedVersion},
		{name: "no version token", raw: "confirm|reg_1", wantErr: ErrUnsupportedVersion},
		{name: "too few fields", raw: "v1|confirm", wantErr: ErrMalformed},
		{name: "too many fields", raw: "v1|confirm|reg_1|ev_1|extra", wantErr: ErrMalformed},
		{name: "empty registration id", raw: "v1|confirm|", wantErr: ErrMalformed},
		{name: "unknown action code", raw: "v1|teleport|reg_1", wantErr: ErrUnknownAction},
		{name: "button text is not a command", raw: "Приду", wantErr: ErrUnsupportedVersion},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(tc.raw)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Decode(%q) error = %v, want %v", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode(%q) unexpected error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("Decode(%q) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestRoundTrip is the property that matters most: whatever the renderer puts
// on a button, the webhook handler must get back unchanged.
func TestRoundTrip(t *testing.T) {
	registrationIDs := []string{"registration_15", "r", "9f8e7d6c-1111-2222-3333-444455556666", strings.Repeat("a", 400)}
	eventIDs := []string{"", "event_42", strings.Repeat("b", 400)}

	for _, action := range domain.AllActionTypes() {
		for _, registrationID := range registrationIDs {
			for _, eventID := range eventIDs {
				encoded, err := Encode(action, registrationID, eventID)
				if err != nil {
					t.Fatalf("Encode(%s, %q, %q): %v", action, registrationID, eventID, err)
				}

				decoded, err := Decode(encoded)
				if err != nil {
					t.Fatalf("Decode(%q): %v", encoded, err)
				}
				if decoded.Action != action {
					t.Errorf("action round-trip: got %q, want %q", decoded.Action, action)
				}
				if decoded.RegistrationID != registrationID {
					t.Errorf("registration id round-trip: got %q, want %q", decoded.RegistrationID, registrationID)
				}
				if decoded.EventID != eventID {
					t.Errorf("event id round-trip: got %q, want %q", decoded.EventID, eventID)
				}
			}
		}
	}
}

// TestEveryActionHasACode guards the two maps against drifting apart: a new
// domain action with no wire code would otherwise fail only at runtime, on a
// button a user is looking at.
func TestEveryActionHasACode(t *testing.T) {
	for _, action := range domain.AllActionTypes() {
		code, ok := actionCodes[action]
		if !ok {
			t.Errorf("action %q has no wire code", action)
			continue
		}
		if back, ok := codeActions[code]; !ok || back != action {
			t.Errorf("code %q does not decode back to %q", code, action)
		}
	}
}

func TestMustEncodePanicsOnBadInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustEncode should panic on an invalid action")
		}
	}()
	MustEncode(domain.ActionType("nope"), "reg_1", "")
}
