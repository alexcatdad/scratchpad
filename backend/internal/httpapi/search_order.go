package httpapi

import "fmt"

// searchCanonicalUTC returns an exact, binary-sortable UTC timestamp or NULL.
// Unsupported historical formats remain on the Go comparison path. Calendar
// arithmetic avoids database date parsing, which differs for year zero and can
// coerce invalid dates; integer casts only execute after the strict shape guard.
func searchCanonicalUTC(engine, expression string) string {
	value := "(" + expression + ")"
	shape := ""
	collation := "BINARY"
	if engine == "postgres" {
		shape = value + ` ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}(:[0-9]{2}([.][0-9]+)?)?Z$' AND right(` + value + `,1)='Z'`
		collation = `"C"`
	} else {
		minute := "[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]"
		second := minute + ":[0-9][0-9]"
		// All accepted characters are ASCII. Byte length also catches NUL tails
		// that SQLite's text length and GLOB functions otherwise truncate.
		shape = fmt.Sprintf(`length(CAST(%[1]s AS BLOB))=length(%[1]s) AND ((length(%[1]s)=17 AND %[1]s GLOB '%[2]sZ') OR (length(%[1]s)=20 AND %[1]s GLOB '%[3]sZ') OR (length(%[1]s)>=22 AND substr(%[1]s,1,19) GLOB '%[3]s' AND substr(%[1]s,20,1)='.' AND substr(%[1]s,length(%[1]s),1)='Z' AND substr(%[1]s,21,length(%[1]s)-21) NOT GLOB '*[^0-9]*'))`, value, minute, second)
	}
	part := func(start, length int) string {
		return fmt.Sprintf("CAST(substr(%s,%d,%d) AS INTEGER)", value, start, length)
	}
	year, month, day := part(1, 4), part(6, 2), part(9, 2)
	hour, minute := part(12, 2), part(15, 2)
	second := fmt.Sprintf("CASE WHEN length(%s)=17 THEN 0 ELSE %s END", value, part(18, 2))
	leap := fmt.Sprintf("%[1]s %% 4=0 AND (%[1]s %% 100<>0 OR %[1]s %% 400=0)", year)
	days := fmt.Sprintf("CASE %s WHEN 2 THEN CASE WHEN %s THEN 29 ELSE 28 END WHEN 4 THEN 30 WHEN 6 THEN 30 WHEN 9 THEN 30 WHEN 11 THEN 30 ELSE 31 END", month, leap)
	calendar := fmt.Sprintf("%s BETWEEN 1 AND 12 AND %s BETWEEN 1 AND (%s) AND %s BETWEEN 0 AND 23 AND %s BETWEEN 0 AND 59 AND (%s) BETWEEN 0 AND 59", month, day, days, hour, minute, second)
	canonical := fmt.Sprintf("CASE WHEN length(%[1]s)=17 THEN substr(%[1]s,1,16)||':00.' ELSE substr(%[1]s,1,19)||'.'||CASE WHEN length(%[1]s)>20 THEN rtrim(substr(%[1]s,21,length(%[1]s)-21),'0') ELSE '' END END", value)
	return fmt.Sprintf("(CASE WHEN %s THEN CASE WHEN %s THEN %s ELSE NULL END ELSE NULL END) COLLATE %s", shape, calendar, canonical, collation)
}
