// Browser drafts are unsent input, never accepted event data. A document owns
// only its own key: two tabs must not replace or erase each other's drafts.
const prefix = 'tam-unsent:';
let ownKey;
let lastRows;
let problem = '';

function key() {
	ownKey ||= `${prefix}${location.pathname}:${Array.from(crypto.getRandomValues(new Uint8Array(16)), (v) => v.toString(16).padStart(2, '0')).join('')}`;
	return ownKey;
}

function notify() {
	window.dispatchEvent(new Event('tam-drafts'));
}

function values(row) {
	if ('t_id' in row) {
		const { prefix, t_id, first_name, last_name, phone_number, pref } = row;
		return { prefix, t_id, first_name, last_name, phone_number, pref };
	}
	const { prefix, b_id, description, donors, winning_ticket } = row;
	return location.pathname.includes('/drawing/')
		? { prefix, b_id, winning_ticket: Number.isFinite(winning_ticket) ? winning_ticket : null }
		: { prefix, b_id, description, donors };
}

export function preserveDraft(rows) {
	try {
		const saved = rows.map(values);
		const serialized = JSON.stringify(saved);
		if (serialized === lastRows && !problem) {
			if (!saved.length) return true;
			const retained = localStorage.getItem(key());
			if (retained && JSON.stringify(JSON.parse(retained).rows) === serialized) return true;
		}
		if (saved.length) localStorage.setItem(key(), JSON.stringify({ page: location.pathname, savedAt: new Date().toISOString(), rows: saved }));
		else if (ownKey) localStorage.removeItem(ownKey);
		lastRows = serialized;
		problem = '';
		notify();
		return true;
	} catch {
		problem = 'TAM could not keep a browser copy of these unsent edits. Keep this page open until Save Marked succeeds.';
		notify();
		return false;
	}
}

export function draftProblem() { return problem; }

export function previousDrafts() {
	const found = [];
	try {
		for (let i = 0; i < localStorage.length; i++) {
			const id = localStorage.key(i);
			if (!id?.startsWith(prefix) || id === ownKey) continue;
			try {
				const draft = JSON.parse(localStorage.getItem(id));
				if (draft.page === location.pathname && Array.isArray(draft.rows) && draft.rows.length) found.push({ ...draft, key: id });
			} catch { /* Leave unreadable data untouched. */ }
		}
	} catch {
		problem = 'TAM could not read browser copies of unsent edits. Keep this page open until Save Marked succeeds.';
	}
	return found;
}

export function removeDraftRow(id, index, expected) {
	try {
		const draft = JSON.parse(localStorage.getItem(id));
		// Another review tab may have already used/discarded this row. Never
		// remove whichever different row now happens to occupy its index.
		if (JSON.stringify(draft.rows[index]) !== JSON.stringify(expected)) return false;
		draft.rows.splice(index, 1);
		if (draft.rows.length) localStorage.setItem(id, JSON.stringify(draft));
		else localStorage.removeItem(id);
		notify();
		return true;
	} catch {
		return false;
	}
}
