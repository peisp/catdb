// api/edit — facade over EditService bindings.
import { EditService } from '../../bindings/catdb/internal/services'
import type {
  RowChange as BoundRowChange,
  RowChangeResult as BoundRowChangeResult,
} from '../../bindings/catdb/internal/services/models'

export type RowChange = BoundRowChange
export type RowChangeResult = BoundRowChangeResult

export function getPrimaryKey(connId: string, db: string, table: string): Promise<string[]> {
  return EditService.GetPrimaryKey(connId, db, table) as unknown as Promise<string[]>
}

export function applyChange(connId: string, change: RowChange): Promise<RowChangeResult> {
  return EditService.ApplyChange(connId, change) as unknown as Promise<RowChangeResult>
}
