import { CaretDown, CaretUp } from "@phosphor-icons/react";
import { cn } from "@/lib/cn";
import type { SortCol, SortDir } from "@/lib/rrset";
import { TH } from "@/components/ui/table";

export function SortHeader({
  col,
  label,
  sort,
  onSort,
}: {
  col: SortCol;
  label: string;
  sort: { col: SortCol; dir: SortDir };
  onSort: (col: SortCol) => void;
}) {
  const active = sort.col === col;
  return (
    <TH aria-sort={active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"} className="p-0">
      <button
        type="button"
        onClick={() => onSort(col)}
        className={cn(
          "flex w-full items-center gap-1 px-3 py-2 text-left font-medium",
          active ? "text-foreground" : "hover:text-foreground",
        )}
      >
        {label}
        {active ? (
          sort.dir === "asc" ? (
            <CaretUp size={12} weight="bold" />
          ) : (
            <CaretDown size={12} weight="bold" />
          )
        ) : (
          <CaretUp size={12} className="opacity-0" />
        )}
      </button>
    </TH>
  );
}
