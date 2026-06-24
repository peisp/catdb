// api/savedQuery — front-end facade over SavedQueryService bindings.
//
// Components import from here, never from `bindings/` directly (CLAUDE.md #1).
// Saved queries are named SQL snippets shown under a database node's 「查询」
// group in the object tree.
import { SavedQueryService } from '../../bindings/catdb/internal/services'
import { SavedQuery as BoundSavedQuery } from '../../bindings/catdb/internal/storage/models'

export type SavedQuery = BoundSavedQuery

/** A draft for Save: id empty → insert, id set → update. */
export interface SavedQueryDraft {
  id?: string
  connId: string
  dbName: string
  name: string
  sqlText: string
  sortOrder?: number
}

export function list(connId: string, db: string): Promise<SavedQuery[]> {
  return SavedQueryService.List(connId, db)
}

export function save(draft: SavedQueryDraft): Promise<SavedQuery> {
  return SavedQueryService.Save(BoundSavedQuery.createFrom(draft))
}

export function del(id: string): Promise<void> {
  return SavedQueryService.Delete(id)
}
