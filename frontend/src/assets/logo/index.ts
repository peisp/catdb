// Driver-type → brand logo (raw SVG for AppIcon). Drivers without a logo
// fall back to the generic database-zap icon.
import mysqlLogo from './mysql.svg?raw'
import postgresqlLogo from './postgresql.svg?raw'
import sqliteLogo from './sqlite.svg?raw'
import databaseZapIcon from '../icons/database-zap.svg?raw'

const LOGOS: Record<string, string> = {
  mysql: mysqlLogo,
  postgres: postgresqlLogo,
  sqlite: sqliteLogo,
}

export function driverLogo(driver: string): string {
  return LOGOS[driver] ?? databaseZapIcon
}
