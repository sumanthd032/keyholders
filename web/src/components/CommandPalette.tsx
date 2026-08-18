"use client";

import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";

type Command = {
  id: string;
  label: string;
  hint: string;
  run: (router: ReturnType<typeof useRouter>) => void;
};

const commands: Command[] = [
  { id: "audit", label: "go to audit", hint: "upload a lockfile", run: (r) => r.push("/") },
  {
    id: "incident",
    label: "go to incident",
    hint: "blast radius for a package@version",
    run: (r) => r.push("/incident"),
  },
  {
    id: "observatory",
    label: "go to observatory",
    hint: "reach across the whole ingested graph",
    run: (r) => r.push("/observatory"),
  },
];

/**
 * The whole tool operable from the keyboard, not only each view's own controls: Cmd/Ctrl+K opens a
 * filterable list of commands, arrow keys move through it, Enter runs the highlighted one, Escape
 * closes it. Scoped to navigation for now, the one action every view shares; a per-view command,
 * "new audit", "skip roll call", would need each view to register into a shared command registry
 * rather than this component knowing about every view's internals, which is a real gap, not
 * something this claims to solve.
 */
export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [highlighted, setHighlighted] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const router = useRouter();

  const filtered = commands.filter((c) => c.label.includes(query.toLowerCase()));

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        setOpen((v) => !v);
        setQuery("");
        setHighlighted(0);
      } else if (e.key === "Escape") {
        setOpen(false);
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  if (!open) return null;

  function run(cmd: Command) {
    cmd.run(router);
    setOpen(false);
  }

  return (
    <div
      className="fixed inset-0 bg-black/60 flex items-start justify-center pt-32 z-50"
      onClick={() => setOpen(false)}
    >
      <div
        role="dialog"
        aria-label="command palette"
        className="w-full max-w-md bg-surface-1 border border-rule-2 rounded-sm overflow-hidden"
        onClick={(e) => e.stopPropagation()}
      >
        <input
          ref={inputRef}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setHighlighted(0);
          }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") {
              e.preventDefault();
              setHighlighted((h) => Math.min(h + 1, filtered.length - 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setHighlighted((h) => Math.max(h - 1, 0));
            } else if (e.key === "Enter" && filtered[highlighted]) {
              run(filtered[highlighted]);
            }
          }}
          placeholder="type a command"
          className="w-full bg-transparent border-b border-rule-1 px-4 py-3 text-sm text-ramp-100 outline-none"
        />
        <ul role="listbox">
          {filtered.length === 0 && <li className="px-4 py-3 text-xs text-ramp-40">no matches</li>}
          {filtered.map((c, i) => (
            <li key={c.id} role="option" aria-selected={i === highlighted}>
              <button
                onClick={() => run(c)}
                onMouseEnter={() => setHighlighted(i)}
                className={`w-full text-left px-4 py-2 flex items-baseline justify-between ${
                  i === highlighted ? "bg-surface-2 text-signal" : "text-ramp-80"
                }`}
              >
                <span className="text-sm">{c.label}</span>
                <span className="text-[11px] text-ramp-40">{c.hint}</span>
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
