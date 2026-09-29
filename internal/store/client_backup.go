package store

// ExportClientBackup includes the ownership metadata needed to restore a
// workstation without inventing entries for forms it never filled in. The
// original three lists remain present for older readers.
func (s *Store) ExportClientBackup() (RecoverySnapshot, error) {
	return s.ExportRecovery()
}

// ImportClientBackup restores only the components actually present in a
// workstation's backup. A legacy file without ownership metadata is a full
// backup; its empty fields remain explicit values, as in the original app.
func (s *Store) ImportClientBackup(snapshot RecoverySnapshot) error {
	if err := ValidateRecoverySnapshot(&snapshot); err != nil {
		return err
	}
	components, err := recoveryComponents(snapshot)
	if err != nil {
		return err
	}
	candidates, err := snapshotCandidates(snapshot)
	if err != nil {
		return err
	}
	return s.WithoutRevisions(func(view *Store) error {
		// A workstation file restore reinstates that file's exact provenance.
		// It is not a delayed network receipt for a current local operation.
		// Keep unrelated components, but never attach an old failed head or
		// conflict to an imported value merely because their payloads match.
		identities := map[string]RecordRevision{}
		for _, candidate := range candidates {
			r := candidate.Revision
			identities[recordKey(r.Kind, r.Prefix, r.ID)] = r
		}
		for _, conflict := range snapshot.Conflicts {
			r := RecordRevision{Kind: conflict.Kind, Prefix: conflict.Prefix, ID: conflict.ID}
			identities[recordKey(r.Kind, r.Prefix, r.ID)] = r
		}
		for _, r := range identities {
			for _, table := range []string{"record_revisions", "record_conflicts"} {
				if _, err := view.exec(`DELETE FROM `+table+` WHERE kind=? AND prefix=? AND record_id=?`, r.Kind, r.Prefix, r.ID); err != nil {
					return err
				}
			}
		}
		if err := view.UpsertPrefixes(snapshot.Prefixes); err != nil {
			return err
		}
		if err := view.UpsertTickets(snapshot.Tickets); err != nil {
			return err
		}
		for _, basket := range snapshot.Baskets {
			owned := components[basketID{basket.Prefix, basket.BID}]
			if owned.Metadata {
				metadata := basket
				metadata.WinningTicket = 0
				if err := view.UpsertBaskets([]Basket{metadata}); err != nil {
					return err
				}
			}
			if owned.Drawing {
				if err := view.UpsertWinning([]Basket{basket}); err != nil {
					return err
				}
			}
		}
		for _, prefix := range snapshot.DeletedPrefixes {
			if _, err := view.DeletePrefix(prefix); err != nil {
				return err
			}
		}
		return view.ApplyReceipt(SaveReceipt{Revisions: snapshot.Revisions, Conflicts: snapshot.Conflicts})
	})
}
