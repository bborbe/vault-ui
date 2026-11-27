import { useEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";
import type { SessionResponse, TopicDetailResponse, TopicResponse } from "../types";

// Dialog is the one blocking question the board asks: end a running turn, name
// a close-out reason, or answer a task's open questions before approving it.
export type Dialog =
  | { type: "takeover" }
  | { type: "reason"; kind: "task" | "goal"; verb: "abort" }
  | { type: "approve"; questions: { index: number; text: string }[] };

export interface ReasonResult {
  reason: string;
  gate_successor: string;
}

export interface ApproveResult {
  answers: { index: number; answer: string }[];
}

// SessionModal shows the hand-off command a Start, Resume or take-over
// produced. It is not a question: it only has to be dismissible.
export function SessionModal({
  session,
  onClose,
}: {
  session: SessionResponse;
  onClose: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const command = session.command || "";

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard access can be refused; the command stays selectable.
    }
  };

  return (
    <div
      id="session-modal"
      className="modal"
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="modal-content">
        <h2>Session Ready</h2>
        <p id="status-message" style={statusStyle(session)}>
          {session.success === true
            ? "✓ Command completed successfully"
            : session.success === false
              ? `✗ Command failed${session.error ? `: ${session.error}` : ""}`
              : ""}
        </p>
        <p>
          <strong>Task:</strong> <code id="task-title">{session.task_title || "Unknown"}</code>
        </p>
        <p id="executed-command-text">
          <code id="executed-command">{session.executed_command || "/work-on-task"}</code> executed. Resume to
          continue working.
        </p>
        <p>
          <strong>Session ID:</strong> <code id="session-id">{session.session_id}</code>
        </p>
        <p>
          <strong>Run this command:</strong>
        </p>
        <code id="handoff-command" className="command-block">
          {command}
        </code>
        <div className="modal-buttons">
          <button type="button" id="copy-btn" onClick={copy}>
            {copied ? "✓ Copied!" : "Copy Command"}
          </button>
          <button type="button" id="close-btn" onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  );
}

function statusStyle(session: SessionResponse): CSSProperties {
  if (session.success === undefined) return { display: "none" };
  return session.success
    ? { display: "block", padding: 8, borderRadius: 4, marginBottom: 12, background: "#d4edda", color: "#155724" }
    : { display: "block", padding: 8, borderRadius: 4, marginBottom: 12, background: "#f8d7da", color: "#721c24" };
}

// LoadingModal covers a task launch while the command runs. Closing it only
// hides the overlay — the command continues in the background.
export function LoadingModal({ onClose }: { onClose: () => void }) {
  return (
    <div
      id="loading-modal"
      className="modal"
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="modal-content" style={{ textAlign: "center", position: "relative" }}>
        <button
          type="button"
          id="close-loading-btn"
          style={{
            position: "absolute",
            top: "0.5rem",
            right: "0.5rem",
            background: "transparent",
            border: "none",
            color: "#9ca3af",
            cursor: "pointer",
            fontSize: "1.5rem",
            lineHeight: 1,
            padding: "0.25rem 0.5rem",
          }}
          onClick={onClose}
        >
          ×
        </button>
        <h2>Executing Command...</h2>
        <p>Please wait while the command is being executed.</p>
        <p style={{ fontSize: "0.9rem", color: "#999", marginTop: 10 }}>This may take up to 2 minutes</p>
        <div
          className="spinner"
          style={{
            margin: "20px auto",
            width: 40,
            height: 40,
            border: "4px solid #f3f3f3",
            borderTop: "4px solid #3498db",
            borderRadius: "50%",
            animation: "spin 1s linear infinite",
          }}
        />
        <p style={{ fontSize: "0.85rem", color: "#999", marginTop: 20 }}>Command continues in background if closed</p>
      </div>
    </div>
  );
}

// ReasonModal collects the abort reason and the gate successor. Confirm stays
// disabled while the reason is blank, so a blank reason can never be submitted.
export function ReasonModal({
  dialog,
  onResolve,
}: {
  dialog: Extract<Dialog, { type: "reason" }>;
  onResolve: (result: ReasonResult | null) => void;
}) {
  const [reason, setReason] = useState("");
  const [gate, setGate] = useState("");
  const inputRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const kindLabel = dialog.kind === "goal" ? "Goal" : "Task";
  const verbLabel = dialog.verb === "abort" ? "Abort" : "Complete";
  const noun = dialog.kind === "goal" ? "goal" : "task";

  return (
    <div
      id="reason-modal"
      className="modal"
      onClick={(event) => {
        if (event.target === event.currentTarget) onResolve(null);
      }}
    >
      <div className="modal-content">
        <h2 id="reason-title">{`${verbLabel} ${kindLabel}`}</h2>
        <p id="reason-risk-prompt">
          {`Does this ${noun} own a trigger, gate, threshold or recurring check? If so, name where it moves (gate successor), or 'none'.`}
        </p>
        <div className="reason-form">
          <label htmlFor="reason-input">Reason (required)</label>
          <textarea
            id="reason-input"
            ref={inputRef}
            rows={3}
            placeholder="Why is this work being closed out?"
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          />
          <label htmlFor="gate-successor-input">Gate successor</label>
          <input
            type="text"
            id="gate-successor-input"
            placeholder="none"
            value={gate}
            onChange={(event) => setGate(event.target.value)}
          />
        </div>
        <div className="modal-buttons">
          <button type="button" id="reason-cancel-btn" onClick={() => onResolve(null)}>
            Cancel
          </button>
          <button
            type="button"
            id="reason-confirm-btn"
            disabled={reason.trim() === ""}
            onClick={() => onResolve({ reason: reason.trim(), gate_successor: gate.trim() || "none" })}
          >
            Confirm
          </button>
        </div>
      </div>
    </div>
  );
}

// ApproveModal builds one labelled field per open question. Only the fields the
// operator actually filled are returned; a cancel returns null and leaves the
// task file untouched.
export function ApproveModal({
  dialog,
  onResolve,
}: {
  dialog: Extract<Dialog, { type: "approve" }>;
  onResolve: (result: ApproveResult | null) => void;
}) {
  const [answers, setAnswers] = useState<Record<number, string>>({});
  const firstRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    firstRef.current?.focus();
  }, []);

  const confirm = () => {
    const filled = Object.entries(answers)
      .filter(([, answer]) => answer.trim() !== "")
      .map(([index, answer]) => ({ index: Number(index), answer: answer.trim() }));
    onResolve({ answers: filled });
  };

  return (
    <div
      id="approve-modal"
      className="modal"
      data-testid="approve-modal"
      onClick={(event) => {
        if (event.target === event.currentTarget) onResolve(null);
      }}
    >
      <div className="modal-content">
        <h2 id="approve-title">Approve Task</h2>
        <p id="approve-prompt">
          {dialog.questions.length === 0
            ? "Approving this task moves it to Planning. It has no open questions."
            : "Approving this task moves it to Planning. Answer what it is waiting on, or leave a field blank."}
        </p>
        <div className="reason-form" id="approve-form">
          {dialog.questions.map((question, position) => (
            <div key={question.index}>
              <label htmlFor={`approve-answer-${question.index}`}>{question.text}</label>
              <textarea
                id={`approve-answer-${question.index}`}
                ref={position === 0 ? firstRef : undefined}
                rows={2}
                data-testid="approve-question"
                data-index={question.index}
                value={answers[question.index] ?? ""}
                onChange={(event) =>
                  setAnswers((current) => ({ ...current, [question.index]: event.target.value }))
                }
              />
            </div>
          ))}
        </div>
        <div className="modal-buttons">
          <button type="button" id="approve-cancel-btn" data-testid="approve-cancel" onClick={() => onResolve(null)}>
            Cancel
          </button>
          <button type="button" id="approve-confirm-btn" data-testid="approve-confirm" onClick={confirm}>
            Approve
          </button>
        </div>
      </div>
    </div>
  );
}

// TakeOverModal is the destructive-action gate: a cancel performs no action.
export function TakeOverModal({ onResolve }: { onResolve: (confirmed: boolean) => void }) {
  return (
    <div
      id="takeover-modal"
      className="modal"
      onClick={(event) => {
        if (event.target === event.currentTarget) onResolve(false);
      }}
    >
      <div className="modal-content">
        <h2 id="takeover-title">Take Over Session</h2>
        <p id="takeover-prompt">
          End the running turn and resume this session? In-flight work is lost unless already saved.
        </p>
        <div className="modal-buttons">
          <button type="button" id="takeover-cancel-btn" onClick={() => onResolve(false)}>
            Cancel
          </button>
          <button type="button" id="takeover-confirm-btn" onClick={() => onResolve(true)}>
            End &amp; Resume
          </button>
        </div>
      </div>
    </div>
  );
}

// TopicModal shows a topic's tracked work, resolved server-side into goals,
// tasks and unresolved names. A resolved entry links into the view that already
// renders that kind, so a topic is a way in to the existing board.
export function TopicModal({
  topic,
  detail,
  error,
  onClose,
  onNavigate,
}: {
  topic: TopicResponse;
  detail: TopicDetailResponse | null;
  error: string | null;
  onClose: () => void;
  onNavigate: (view: "goals" | "tasks") => void;
}) {
  const section = (label: string, names: string[], view: "goals" | "tasks" | null, empty: string) => (
    <>
      <h3>{`${label} (${names.length})`}</h3>
      {names.length === 0 ? (
        <p className="topic-empty">{empty}</p>
      ) : (
        <ul className="topic-entries">
          {names.map((name) => (
            <li key={name}>
              {view === null ? (
                name
              ) : (
                <a
                  href={`?view=${view}`}
                  onClick={(event) => {
                    event.preventDefault();
                    onNavigate(view);
                  }}
                >
                  {name}
                </a>
              )}
            </li>
          ))}
        </ul>
      )}
    </>
  );

  return (
    <div
      id="topic-modal"
      className="modal"
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="modal-content">
        <div className="topic-modal-header">
          <h2 id="topic-modal-title">{topic.title || topic.id}</h2>
          {topic.status && <span id="topic-modal-status" className="status-badge">{topic.status}</span>}
        </div>
        <div id="topic-modal-body">
          {error !== null ? (
            <p className="topic-empty">{`Failed to load: ${error}`}</p>
          ) : detail === null ? (
            <p className="topic-empty">Loading…</p>
          ) : (
            <>
              {section("Goals", detail.goals ?? [], "goals", "No goal entries.")}
              {section("Tasks", detail.tasks ?? [], "tasks", "No task entries.")}
              {(detail.unresolved ?? []).length > 0 && section("Unresolved", detail.unresolved, null, "")}
            </>
          )}
        </div>
        <div className="modal-buttons">
          <button type="button" id="topic-modal-close-btn" onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  );
}

export interface ToastMessage {
  id: number;
  message: string;
  error: boolean;
}

export function Toasts({ toasts }: { toasts: ToastMessage[] }) {
  return (
    <>
      {toasts.map((toast) => (
        <div key={toast.id} className={`toast${toast.error ? " error" : ""}`}>
          {toast.message}
        </div>
      ))}
    </>
  );
}
