## Сборка проекта
wails build -clean -trimpath -ldflags "-s -w"

## Portable-режим

Создайте папку `data` рядом с `outline-reader.exe`. База данных, профиль WebView2 (тема, ширина панели, раскрытые узлы) будут храниться внутри неё — `%APPDATA%` не используется.

Если папки `data` нет или она недоступна для записи, приложение работает в обычном режиме (данные в `%APPDATA%\OutlineReader`).

Готовый portable-архив `outline-reader-*-portable-windows-amd64.zip` уже содержит пустую папку `data`.