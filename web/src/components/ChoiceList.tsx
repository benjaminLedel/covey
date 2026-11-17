/* A short list of choices, each with one sentence of what it means (#743).
   Used where a select hid the meaning of the options: a role, the level of an
   account. The native radio stays in the markup for keyboard and screen
   readers; only its look is replaced. */
export interface Choice {
  value: string;
  label: string;
  desc?: string;
}

export default function ChoiceList({
  name,
  legend,
  value,
  onChange,
  choices,
  current,
  currentLabel,
}: {
  name: string;
  legend: string;
  value: string;
  onChange: (v: string) => void;
  choices: Choice[];
  /** The value in force now, marked so the change is visible. */
  current?: string;
  currentLabel?: string;
}) {
  return (
    <fieldset className="choice-list">
      <legend className="sr-only">{legend}</legend>
      {choices.map((c) => (
        <label key={c.value} className={`choice${value === c.value ? " on" : ""}`}>
          <input type="radio" name={name} value={c.value} checked={value === c.value} onChange={() => onChange(c.value)} />
          <span className="choice-dot" aria-hidden="true" />
          <span className="min-w-0">
            <span className="choice-label">
              {c.label}
              {current === c.value && currentLabel && <span className="choice-now">{currentLabel}</span>}
            </span>
            {c.desc && <span className="choice-desc">{c.desc}</span>}
          </span>
        </label>
      ))}
    </fieldset>
  );
}
