
@REM 根据`../dsl/api/all.api` 生成 API 代码

set apiDir=../dsl/api
set apiOutDir=../app

goctl api go --api %apiDir%/api.api --dir %apiOutDir%/
