package gateway

func containsToolRow(rows []StoredSessionToolRow, callID, status, name string) bool {
	for _, row := range rows {
		if row.CallID == callID && row.Status == status && row.Name == name {
			return true
		}
	}
	return false
}
