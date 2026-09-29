package store

import (
	"errors"
	"fmt"
)

// ValidateSaveReceipt checks that a successful numbered operation actually
// acknowledges its accepted values before the delivery journal is removed.
func ValidateSaveReceipt(request Outbox, receipt *SaveReceipt) error {
	if receipt == nil || receipt.Revisions == nil {
		return errors.New("native save receipt is missing revisions")
	}
	if request.Order.Client == "" || request.Order.Save <= 0 {
		return errors.New("native save receipt requires a numbered request")
	}
	if len(receipt.Conflicts) != 0 {
		return errors.New("successful save receipt contains unresolved conflicts")
	}
	edits, err := recoveryIntentEdits(&request)
	if err != nil {
		return err
	}
	// A batch may edit one record repeatedly. The committed final value is
	// the last occurrence, matching the store's transactional write behavior.
	expected := make(map[string]recoveryIntentEdit, len(edits))
	for _, edit := range edits {
		expected[edit.key] = edit
	}
	dot := operationKey("client:"+request.Order.Client, request.Order.Save)
	for _, revision := range receipt.Revisions {
		if err := validateRevision(revision); err != nil {
			return fmt.Errorf("invalid save receipt: %w", err)
		}
		key := recordKey(revision.Kind, revision.Prefix, revision.ID)
		edit, exists := expected[key]
		if !exists || revision.Hash != edit.hash || revision.Operations[dot] != edit.hash {
			return errors.New("save receipt does not match the requested value and operation")
		}
		delete(expected, key)
	}
	for _, edit := range expected {
		// Metadata updates intentionally ignore the supplied winner for an
		// existing basket. A drawing receipt exists only for a new insertion.
		if !edit.insertWinner {
			return errors.New("save receipt does not acknowledge every requested value")
		}
	}
	return nil
}
