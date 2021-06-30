// Copyright (c) 2021 Terminus, Inc.
//
// This program is free software: you can use, redistribute, and/or modify
// it under the terms of the GNU Affero General Public License, version 3
// or later ("AGPL"), as published by the Free Software Foundation.
//
// This program is distributed in the hope that it will be useful, but WITHOUT
// ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
// FITNESS FOR A PARTICULAR PURPOSE.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package secret

import (
	"math/rand"
	"time"

)

var fontKinds = [][]int{{10, 48}, {26, 97}, {26, 65}}


func RandStr(size int) string {
	result := make([]byte, size)
	rand.Seed(time.Now().UnixNano())
	for i := 0; i < size; i++ {
		ikind := rand.Intn(3)
		scope, base := fontKinds[ikind][0], fontKinds[ikind][1]
		result[i] = uint8(base + rand.Intn(scope))
	}
	return string(result)
}

type AkSkPair struct {
	AccessKeyID string
	SecretKey   string
}

func CreateAkSkPair() AkSkPair {
	return AkSkPair{
		AccessKeyID: RandStr(24),
		SecretKey:   RandStr(32),
	}
}
