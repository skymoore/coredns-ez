import { Minus, Plus } from "@phosphor-icons/react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { canHaveMultipleValues } from "@/lib/rrset";

export function ValuesField({
  type,
  values,
  placeholder,
  onChange,
}: {
  type: string;
  values: string[];
  placeholder?: string;
  onChange: (values: string[]) => void;
}) {
  const multi = canHaveMultipleValues(type);
  const label = multi ? "Values" : "Value";
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <Label>{label}</Label>
        {multi ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => onChange([...values, ""])}
          >
            <Plus size={14} />
            Add value
          </Button>
        ) : null}
      </div>
      <div className="max-h-48 space-y-2 overflow-y-auto">
        {values.map((v, i) => (
          <div key={i} className="flex gap-2">
            <Input
              id={i === 0 ? "rdata" : undefined}
              value={v}
              onChange={(e) => {
                const next = [...values];
                next[i] = e.target.value;
                onChange(next);
              }}
              placeholder={placeholder}
              required={values.length === 1}
              spellCheck={false}
              autoComplete="off"
            />
            {multi && values.length > 1 ? (
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label="Remove value"
                onClick={() => onChange(values.filter((_, j) => j !== i))}
              >
                <Minus size={16} />
              </Button>
            ) : null}
          </div>
        ))}
      </div>
      {multi ? (
        <p className="text-xs text-muted-foreground">
          Each value is one RR in this set. A records take one address per row; TXT one string per row.
        </p>
      ) : (
        <p className="text-xs text-muted-foreground">{type} can only have one target.</p>
      )}
    </div>
  );
}
