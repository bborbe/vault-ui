import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { Card as CardModel, CardKind } from "../model";

export interface MenuState {
  kind: CardKind;
  card: CardModel;
  anchor: HTMLElement;
}

export type MenuAction =
  | "clear_session"
  | "complete_task"
  | "defer_task"
  | "abort_task"
  | "hold_task"
  | "resume_task"
  | "complete_goal"
  | "defer_goal"
  | "abort_goal"
  | "hold_goal"
  | "resume_goal";

interface MenuItem {
  label: string;
  action: MenuAction;
  disabled?: boolean;
}

// menuItems is the per-kind lifecycle menu. A goal and a task share the shape;
// only the labels and the clear-session wording differ.
function menuItems(kind: CardKind, card: CardModel): MenuItem[] {
  const hasSession = Boolean((card as { claude_session_id?: string }).claude_session_id);
  const onHold = (card as { status?: string }).status === "hold";
  if (kind === "goal") {
    return [
      ...(hasSession ? [{ label: "Reset Session", action: "clear_session" as const }] : []),
      { label: "Complete Goal", action: "complete_goal" },
      { label: "Defer Goal", action: "defer_goal" },
      { label: "Abort Goal", action: "abort_goal" },
      onHold
        ? { label: "Resume Goal", action: "resume_goal" }
        : { label: "Hold Goal", action: "hold_goal" },
    ];
  }
  return [
    ...(hasSession ? [{ label: "Clear Session", action: "clear_session" as const }] : []),
    { label: "Complete Task", action: "complete_task" },
    { label: "Defer Task", action: "defer_task" },
    { label: "Abort Task", action: "abort_task" },
    onHold
      ? { label: "Resume Task", action: "resume_task" }
      : { label: "Hold Task", action: "hold_task" },
  ];
}

// Menu is the card's lifecycle popup. It is positioned next to its trigger,
// flipped up and clamped horizontally so a menu opened near the viewport edge
// stays on screen, and it closes on the first click outside.
export default function Menu({
  state,
  onSelect,
  onClose,
}: {
  state: MenuState;
  onSelect: (action: MenuAction) => void;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);

  useLayoutEffect(() => {
    const menu = ref.current;
    if (!menu) return;
    const rect = state.anchor.getBoundingClientRect();
    const menuRect = menu.getBoundingClientRect();
    let top = rect.bottom + 5;
    if (top + menuRect.height > window.innerHeight) {
      top = rect.top - menuRect.height - 5;
    }
    let left = rect.left - 150;
    if (left < 0) {
      left = 5;
    } else if (left + menuRect.width > window.innerWidth) {
      left = window.innerWidth - menuRect.width - 5;
    }
    setPosition({ top, left });
  }, [state.anchor]);

  useEffect(() => {
    // Capture phase: the trigger's own click must not immediately close the
    // menu it just opened.
    const onClick = (event: MouseEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) {
        event.stopPropagation();
        onClose();
      }
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    const timer = window.setTimeout(() => {
      document.addEventListener("click", onClick, true);
      document.addEventListener("keydown", onKeyDown);
    }, 0);
    return () => {
      window.clearTimeout(timer);
      document.removeEventListener("click", onClick, true);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [onClose]);

  return (
    <div
      ref={ref}
      className="task-menu"
      role="menu"
      style={
        position
          ? { position: "fixed", top: position.top, left: position.left }
          : { position: "fixed", visibility: "hidden" }
      }
    >
      {menuItems(state.kind, state.card).map((item) => (
        <div
          key={item.action}
          className={`task-menu-item${item.disabled ? " disabled" : ""}`}
          onClick={item.disabled ? undefined : () => onSelect(item.action)}
        >
          {item.label}
        </div>
      ))}
    </div>
  );
}
