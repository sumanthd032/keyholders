import Link from "next/link";

const links = [
  { href: "/", label: "audit" },
  { href: "/incident", label: "incident" },
  { href: "/observatory", label: "observatory" },
];

export function Nav({ current }: { current: string }) {
  return (
    <nav className="flex items-baseline gap-4">
      {links
        .filter((l) => l.href !== current)
        .map((l) => (
          <Link
            key={l.href}
            href={l.href}
            className="text-xs text-ramp-40 hover:text-ramp-80 underline underline-offset-2"
          >
            {l.label}
          </Link>
        ))}
      <span className="tabular text-[11px] text-ramp-40 border border-rule-2 rounded-sm px-1.5 py-0.5">
        ⌘K
      </span>
    </nav>
  );
}
