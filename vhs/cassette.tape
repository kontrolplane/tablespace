Set FontSize 20
Set Width 2000
Set Height 1000
Set BorderRadius 10
Set TypingSpeed 100ms

Output ./assets/cassette.gif

Hide
  Type@10ms "go build -o build/kontrolplane/tablespace . && clear && PGPASSWORD=postgres ./build/kontrolplane/tablespace --host localhost --user postgres --dbname shop"
  Enter
  Wait+Screen@60s /connected/
  Sleep 1s
Show

Sleep 5s

Screenshot ./assets/pages/database/overview.png

Sleep 3s
Tab
Sleep 5s

Screenshot ./assets/pages/database/activity.png

Sleep 3s
Down@800ms 1
Sleep 2s
Enter
Sleep 4s

Screenshot ./assets/pages/backend/details.png

Sleep 3s
Type "q"
Sleep 3s
Shift+Tab
Sleep 3s
Down@800ms 2
Sleep 2s
Enter

Sleep 5s

Screenshot ./assets/pages/database/tables.png

Sleep 3s
Down@800ms 5
Sleep 2s
Enter

Sleep 5s

Screenshot ./assets/pages/table/rows.png

Sleep 3s
Down@800ms 1
Sleep 2s
Enter
Sleep 3s
Down@800ms 4
Sleep 4s

Screenshot ./assets/pages/table/row.png

Sleep 3s
Type "q"
Sleep 3s
Type "w"
Sleep 2s
Type "status = 'cancelled' and total > 300"
Sleep 2s
Enter
Sleep 5s

Screenshot ./assets/pages/table/where.png

Sleep 3s
Tab
Sleep 4s

Screenshot ./assets/pages/table/columns.png

Sleep 3s
Ctrl+D
Sleep 2s
Type "shop.orders"
Sleep 3s

Screenshot ./assets/pages/table/drop.png

Sleep 3s
Escape
Sleep 3s
Type "q"
Sleep 3s
Type "e"
Sleep 2s
Type@60ms "select status, count(*), sum(total) as revenue from shop.orders group by 1 order by 3 desc;"
Sleep 2s
Ctrl+S
Sleep 5s

Screenshot ./assets/pages/sql/editor.png

Sleep 3s
Escape
Sleep 4s
