# 原型存档

## category-tag-facets.html（2026-11-18 定稿 v4）

**问题**：sth 首页的「分类/标签汇总列表」长什么样？三种视图能否共用一份数据源并存？

**迭代**：v1 分类 chips → v2 加标签 facet → v3 三布局并存（页内分段切换器）→ v4 切换器改下拉框（定稿）。

**结论**：三种布局（blocks / sidebar / overview）共用同一份 facet 数据并存，右上角「视图」下拉切换，布局选择存 cookie；计数互为 facet（分类计数不受自身过滤影响，标签反之）；含「未分类」桶；标签长尾 Top 20 截断。已按此实现：PR sun-praise/static-html#99。

**查看方式**：直接用浏览器打开本 HTML（数据为 mock、零依赖）；或 `sth send prototypes/category-tag-facets.html --title "..."` 发到任意 sth 实例预览。键盘 ←/→ 或底部浮条可切换布局变体（?variant=A|B|C）。
