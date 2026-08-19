import Link from "next/link";

/** Any URL outside the three named views lands here rather than a blank page. */
export default function NotFound() {
  return (
    <div className="flex-1 flex flex-col items-center justify-center gap-3 px-8">
      <p className="text-sm text-ramp-80">nothing here</p>
      <Link
        href="/"
        className="text-xs text-ramp-40 hover:text-ramp-80 underline underline-offset-2"
      >
        back to audit
      </Link>
    </div>
  );
}
