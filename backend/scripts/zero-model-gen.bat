@REM 根据`../sql/*.sql` 生成数据库 Model 代码

set sqlDir=../sql
set modelOutDir=../app/internal/model

goctl model mysql ddl --src %sqlDir%/a1_user.sql            --dir %modelOutDir%/auth            --cache
goctl model mysql ddl --src %sqlDir%/a2_rbac.sql            --dir %modelOutDir%/rbac            --cache
goctl model mysql ddl --src %sqlDir%/a3_announcement.sql    --dir %modelOutDir%/announcement    --cache
goctl model mysql ddl --src %sqlDir%/a4_markdown.sql        --dir %modelOutDir%/markdown        --cache