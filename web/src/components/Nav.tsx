import Link from "next/link";

const links = [
  { href: "/", label: "audit" },
  { href: "/incident", label: "incident" },
  { href: "/observatory", label: "observatory" },
];

export function Nav({ current }: { current: string }) {
  return (
    <nav className="flex gap-4">
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
    </nav>
  );
}
