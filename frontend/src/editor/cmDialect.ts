// editor/cmDialect — maps a driver's UIDialect.editorDialect id onto the
// matching @codemirror/lang-sql dialect. Unknown ids fall back to StandardSQL
// so a future driver renders sensibly before we add its mapping.
import {
  MSSQL,
  MariaSQL,
  MySQL,
  PLSQL,
  PostgreSQL,
  SQLite,
  StandardSQL,
  type SQLDialect,
} from '@codemirror/lang-sql'

const BY_ID: Record<string, SQLDialect> = {
  mysql: MySQL,
  mariadb: MariaSQL,
  postgresql: PostgreSQL,
  sqlite: SQLite,
  mssql: MSSQL,
  plsql: PLSQL,
  standard: StandardSQL,
}

export function cmSqlDialect(id?: string): SQLDialect {
  return BY_ID[(id ?? '').toLowerCase()] ?? StandardSQL
}
