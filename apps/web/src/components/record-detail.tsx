import { useState } from "react";
import { api, label, type MemoryRecord, post } from "../lib/api";
import { encodedId } from "../lib/opaque-id";

type Snapshot = Record<string, unknown>;
type Relationship = {
  id: string;
  fromRecordId: string;
  toRecordId: string;
  type: string;
  authority: string;
  status: string;
  note?: string;
  version: number;
};
export type Detail = {
  record: MemoryRecord;
  metadata: {
    displayTitle?: string;
    tags?: string[];
    curatedSummary?: string;
    archived?: boolean;
    version: number;
  };
  revisions: {
    id: string;
    createdAt: string;
    reason?: string;
    previous?: Snapshot;
    next?: Snapshot;
    actor?: Snapshot;
  }[];
  evidence: {
    id: string;
    kind: string;
    reference: string;
    description?: string;
  }[];
  relationships: Relationship[];
  audit: {
    id: string;
    action: string;
    createdAt: string;
    actor?: Snapshot;
    previous?: unknown;
    next?: unknown;
  }[];
};
const relationshipTypes = [
  "related_to",
  "supports",
  "contradicts",
  "refines",
  "replaces",
  "partially_replaces",
  "depends_on",
  "implements",
  "caused_by",
  "answers",
];
function Json({ value }: { value: unknown }) {
  return <pre className="audit-json">{JSON.stringify(value, null, 2)}</pre>;
}
export function RecordDetail({
  detail,
  onClose,
  onChanged,
  onNavigate,
}: {
  detail: Detail;
  onClose: () => void;
  onChanged: () => void;
  onNavigate: (id: string) => void;
}) {
  const { record, metadata } = detail;
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [matches, setMatches] = useState<MemoryRecord[]>([]);
  const [target, setTarget] = useState("");
  async function perform(work: () => Promise<unknown>) {
    setError("");
    setBusy(true);
    try {
      await work();
      onChanged();
    } catch (reason) {
      setError(
        reason instanceof Error ? reason.message : "Could not save change.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <aside className="detail" aria-label="Record detail">
      <button className="close-detail" type="button" onClick={onClose}>
        Close
      </button>
      <span className="record-type">{label(record.type)}</span>
      <h2>{metadata.displayTitle || record.title}</h2>
      {metadata.archived && <p>Archived from project context.</p>}
      {metadata.curatedSummary && (
        <section>
          <h3>Curated summary</h3>
          <p className="preserve">{metadata.curatedSummary}</p>
          <small>Added after the original capture.</small>
        </section>
      )}
      {Object.entries(record.payload).map(([key, value]) =>
        key === "legacyOriginal" ? (
          <details key={key}>
            <summary>Original imported entry</summary>
            <Json value={value} />
          </details>
        ) : (
          <section key={key}>
            <h3>{label(key)}</h3>
            <p className="preserve break-word">
              {typeof value === "string" ? value : JSON.stringify(value)}
            </p>
          </section>
        ),
      )}
      <dl className="provenance">
        <div>
          <dt>Authority</dt>
          <dd>{label(record.authority)}</dd>
        </div>
        <div>
          <dt>Confidence</dt>
          <dd>{label(record.confidence)}</dd>
        </div>
      </dl>
      {record.confidenceReason && <p>{record.confidenceReason}</p>}
      <details>
        <summary>Capture provenance</summary>
        <p>Captured {new Date(record.recordedAt).toLocaleString("en")}</p>
        {record.happenedAt && <p>Occurred {record.happenedAt}</p>}
        {record.provenance && (
          <>
            <h3>Source provenance</h3>
            <Json value={record.provenance} />
          </>
        )}
        <h3>Recorded by</h3>
        <Json value={record.actor ?? {}} />
        {record.gitContext && (
          <>
            <h3>Git context</h3>
            <Json value={record.gitContext} />
          </>
        )}
        <small className="break-word">{record.id}</small>
      </details>
      <section>
        <h3>Decision chain and relationships</h3>
        {detail.relationships.length ? (
          detail.relationships.map((link) => (
            <div className="relationship" key={link.id}>
              <p>
                {link.fromRecordId === record.id
                  ? "This record"
                  : "Another record"}{" "}
                {label(link.type).toLowerCase()}{" "}
                {link.toRecordId === record.id
                  ? "this record"
                  : "another record"}{" "}
                · {label(link.status)} · {label(link.authority)}
              </p>
              {link.note && <p>{link.note}</p>}
              <button
                type="button"
                onClick={() =>
                  onNavigate(
                    link.fromRecordId === record.id
                      ? link.toRecordId
                      : link.fromRecordId,
                  )
                }
              >
                Open related record
              </button>
              {link.status === "suggested" && (
                <div className="action-row">
                  {["accept", "reject"].map((action) => (
                    <button
                      key={action}
                      type="button"
                      disabled={busy}
                      onClick={() =>
                        void perform(() =>
                          api(
                            `/relationships/${encodedId(link.id)}/${action}`,
                            {
                              ...post({}),
                              headers: { "If-Match": String(link.version) },
                            },
                          ),
                        )
                      }
                    >
                      {label(action)} relationship
                    </button>
                  ))}
                </div>
              )}
            </div>
          ))
        ) : (
          <p className="quiet">No relationships recorded.</p>
        )}
        <details>
          <summary>Link another record</summary>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const form = new FormData(e.currentTarget);
              setError("");
              void api<{ records: MemoryRecord[] }>(
                `/records?q=${encodeURIComponent(String(form.get("query")))}&limit=20`,
              )
                .then((data) =>
                  setMatches(data.records.filter((r) => r.id !== record.id)),
                )
                .catch((reason: Error) => setError(reason.message));
            }}
          >
            <label>
              Find related record
              <input name="query" required />
            </label>
            <button type="submit">Find records</button>
          </form>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const form = new FormData(e.currentTarget);
              void perform(() =>
                api(
                  "/relationships",
                  post({
                    fromRecordId: record.id,
                    toRecordId: target,
                    type: form.get("type"),
                    authority: "explicit",
                    note: form.get("note"),
                  }),
                ),
              );
            }}
          >
            <label>
              Related record
              <select
                aria-label="Related record"
                required
                value={target}
                onChange={(e) => setTarget(e.target.value)}
              >
                <option value="">Choose a record</option>
                {matches.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.title}
                  </option>
                ))}
              </select>
            </label>
            <label>
              This record
              <select name="type" aria-label="Relationship type">
                {relationshipTypes.map((type) => (
                  <option key={type} value={type}>
                    {label(type)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Relationship note
              <textarea name="note" />
            </label>
            <button disabled={busy || !target} type="submit">
              Add relationship
            </button>
          </form>
        </details>
      </section>
      <section>
        <h3>History</h3>
        <p>Captured {new Date(record.recordedAt).toLocaleString("en")}</p>
        {detail.revisions.map((revision) => (
          <details key={revision.id}>
            <summary>
              {new Date(revision.createdAt).toLocaleString("en")} ·{" "}
              {revision.reason || "Metadata updated"}
            </summary>
            <h3>Before</h3>
            <Json value={revision.previous} />
            <h3>After</h3>
            <Json value={revision.next} />
            <h3>Changed by</h3>
            <Json value={revision.actor} />
          </details>
        ))}
        <details>
          <summary>Edit display title</summary>
          <form
            key={`${record.id}-${metadata.version}`}
            onSubmit={(e) => {
              e.preventDefault();
              const form = new FormData(e.currentTarget);
              void perform(() =>
                api(`/records/${encodedId(record.id)}/revisions`, {
                  ...post({
                    changes: {
                      displayTitle: form.get("displayTitle"),
                      tags: String(form.get("tags") ?? "")
                        .split(",")
                        .map((s) => s.trim())
                        .filter(Boolean),
                      curatedSummary: form.get("curatedSummary"),
                      archived: form.get("archived") === "on",
                    },
                    reason: form.get("reason"),
                  }),
                  headers: { "If-Match": String(metadata.version) },
                }),
              );
            }}
          >
            <label>
              Display title
              <input
                name="displayTitle"
                defaultValue={metadata.displayTitle || record.title}
                maxLength={500}
                required
              />
            </label>
            <label>
              Tags, separated by commas
              <input name="tags" defaultValue={metadata.tags?.join(", ")} />
            </label>
            <label>
              Curated summary
              <textarea
                name="curatedSummary"
                defaultValue={metadata.curatedSummary}
              />
            </label>
            <label>
              Reason for change
              <input name="reason" />
            </label>
            <label className="checkbox">
              <input
                type="checkbox"
                name="archived"
                defaultChecked={metadata.archived}
              />
              Archive from project context
            </label>
            <button type="submit" disabled={busy}>
              Save title
            </button>
          </form>
          <small>Original capture remains unchanged.</small>
        </details>
      </section>
      <section>
        <h3>Evidence</h3>
        {detail.evidence.length ? (
          detail.evidence.map((e) => (
            <p key={e.id}>
              {e.description ?? e.kind}:{" "}
              <span className="break-word">{e.reference}</span>
            </p>
          ))
        ) : (
          <p className="quiet">No evidence attached.</p>
        )}
        <details>
          <summary>Add evidence</summary>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              const form = new FormData(e.currentTarget);
              void perform(() =>
                api(
                  `/records/${encodedId(record.id)}/evidence`,
                  post(Object.fromEntries(form)),
                ),
              );
            }}
          >
            <label>
              Evidence kind
              <select name="kind" aria-label="Evidence kind">
                {[
                  "url",
                  "git_commit",
                  "pull_request",
                  "issue",
                  "file",
                  "conversation",
                  "test",
                  "deployment",
                  "other",
                ].map((kind) => (
                  <option key={kind} value={kind}>
                    {label(kind)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Reference
              <input name="reference" required maxLength={4000} />
            </label>
            <label>
              Description
              <input name="description" />
            </label>
            <button type="submit" disabled={busy}>
              Save evidence
            </button>
          </form>
        </details>
      </section>
      <details>
        <summary>Audit trail ({detail.audit.length})</summary>
        {detail.audit.map((event) => (
          <details key={event.id}>
            <summary>
              {label(event.action)} ·{" "}
              {new Date(event.createdAt).toLocaleString("en")}
            </summary>
            <Json
              value={{
                actor: event.actor,
                previous: event.previous,
                next: event.next,
              }}
            />
          </details>
        ))}
      </details>
      {error && (
        <p role="alert" className="error">
          {error}
        </p>
      )}
    </aside>
  );
}
