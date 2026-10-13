package maps // import "maps"

Package maps defines various functions useful with maps of any type.

This package does not have any special handling for non-reflexive keys (keys k
where k != k), such as floating-point NaNs.

FUNCTIONS

func All[Map ~map[K]V, K comparable, V any](m Map) iter.Seq2[K, V]
    All returns an iterator over key-value pairs from m. The iteration order
    is not specified and is not guaranteed to be the same from one call to the
    next.

func Clone[M ~map[K]V, K comparable, V any](m M) M
    Clone returns a copy of m. This is a shallow clone: the new keys and values
    are set using ordinary assignment.

func Collect[K comparable, V any](seq iter.Seq2[K, V]) map[K]V
    Collect collects key-value pairs from seq into a new map and returns it.

func Copy[M1 ~map[K]V, M2 ~map[K]V, K comparable, V any](dst M1, src M2)
    Copy copies all key/value pairs in src adding them to dst. When a key in src
    is already present in dst, the value in dst will be overwritten by the value
    associated with the key in src.

func DeleteFunc[M ~map[K]V, K comparable, V any](m M, del func(K, V) bool)
    DeleteFunc deletes any key/value pairs from m for which del returns true.

func Equal[M1, M2 ~map[K]V, K, V comparable](m1 M1, m2 M2) bool
    Equal reports whether two maps contain the same key/value pairs. Values are
    compared using ==.

func EqualFunc[M1 ~map[K]V1, M2 ~map[K]V2, K comparable, V1, V2 any](m1 M1, m2 M2, eq func(V1, V2) bool) bool
    EqualFunc is like Equal, but compares values using eq. Keys are still
    compared with ==.

func Insert[Map ~map[K]V, K comparable, V any](m Map, seq iter.Seq2[K, V])
    Insert adds the key-value pairs from seq to m. If a key in seq already
    exists in m, its value will be overwritten.

func Keys[Map ~map[K]V, K comparable, V any](m Map) iter.Seq[K]
    Keys returns an iterator over keys in m. The iteration order is not
    specified and is not guaranteed to be the same from one call to the next.

func Values[Map ~map[K]V, K comparable, V any](m Map) iter.Seq[V]
    Values returns an iterator over values in m. The iteration order is not
    specified and is not guaranteed to be the same from one call to the next.

