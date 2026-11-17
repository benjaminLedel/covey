import type { ReactNode } from "react";

/* One section of a settings-like page (#743): a heading, one sentence of what
   it is for, then its content — separated from the next by a hairline, not
   wrapped in a card. Pages built from these stay one level deep. */
export default function Section({
  title,
  desc,
  action,
  children,
}: {
  title: string;
  desc?: ReactNode;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="ps">
      <div className="ps-headline">
        <h2 className="ps-title">{title}</h2>
        {action}
      </div>
      {desc && <p className="ps-desc">{desc}</p>}
      {children}
    </section>
  );
}
