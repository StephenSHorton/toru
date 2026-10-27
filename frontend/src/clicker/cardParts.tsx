import { cn } from "@/lib/utils";

// Small layout pieces shared by the Auto Clicker cards, so every card has the
// same frame, header and row rhythm as the Send Files page.

/** Card is one frosted panel; `wide` spans both grid columns. */
export function Card({
  label,
  wide,
  dim,
  className,
  children,
}: {
  label: string;
  wide?: boolean;
  dim?: boolean;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <section
      aria-label={label}
      className={cn(
        "frost flex flex-col gap-4 border border-border p-4",
        wide && "lg:col-span-2",
        dim && "opacity-60",
        className,
      )}
    >
      {children}
    </section>
  );
}

/** CardHead: icon + title + caption on the left, an optional control on the right. */
export function CardHead({
  icon: Icon,
  title,
  caption,
  children,
}: {
  icon: React.ComponentType<{ className?: string }>;
  title: React.ReactNode;
  caption: React.ReactNode;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex items-start justify-between gap-3">
      <div className="flex min-w-0 items-start gap-2">
        <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="flex min-w-0 flex-col">
          <span className="text-sm font-medium">{title}</span>
          <span className="text-xs text-muted-foreground">{caption}</span>
        </div>
      </div>
      {children ? <div className="flex shrink-0 items-center gap-2">{children}</div> : null}
    </div>
  );
}

/** Row: a label + caption on the left and a control on the right. */
export function Row({
  label,
  caption,
  htmlFor,
  children,
}: {
  label: React.ReactNode;
  caption?: React.ReactNode;
  htmlFor?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="flex min-w-0 flex-col">
        <label htmlFor={htmlFor} className="text-sm font-medium">
          {label}
        </label>
        {caption ? <span className="text-xs text-muted-foreground">{caption}</span> : null}
      </div>
      <div className="flex shrink-0 items-center gap-2">{children}</div>
    </div>
  );
}

/** Note: a muted one-line explanation, or a warning in amber. */
export function Note({ warn, children }: { warn?: boolean; children: React.ReactNode }) {
  return <p className={cn("text-xs", warn ? "text-amber-400" : "text-muted-foreground")}>{children}</p>;
}
