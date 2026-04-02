import { useState, useCallback, useRef, useEffect } from "react";
import { Input } from "@opskat/ui";
import { Search, X } from "lucide-react";
import { useI18n } from "../hooks/useI18n";

interface SearchBarProps {
  value: string;
  onSearch: (query: string) => void;
}

export function SearchBar({ value, onSearch }: SearchBarProps) {
  const { t } = useI18n();
  const [localValue, setLocalValue] = useState(value);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => {
    setLocalValue(value);
  }, [value]);

  const handleChange = useCallback(
    (val: string) => {
      setLocalValue(val);
      if (debounceRef.current) clearTimeout(debounceRef.current);
      debounceRef.current = setTimeout(() => onSearch(val), 300);
    },
    [onSearch],
  );

  const handleClear = useCallback(() => {
    setLocalValue("");
    onSearch("");
  }, [onSearch]);

  return (
    <div className="relative flex items-center">
      <Search className="absolute left-2 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
      <Input
        value={localValue}
        onChange={(e) => handleChange(e.target.value)}
        placeholder={t("browser.searchPlaceholder")}
        className="h-7 w-48 pl-7 pr-7 text-sm"
      />
      {localValue && (
        <button
          onClick={handleClear}
          className="absolute right-2 text-muted-foreground hover:text-foreground"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      )}
    </div>
  );
}
