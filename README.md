# fatima-opm

Juno, Jupiter, fatima-cmd의 공통 운영 API 및 구현입니다. 일반 Fatima 프로그램 런타임은 fatima-core에서 제공합니다. 이 모듈은 fatima-core에 의존하지 않습니다.

- api: protobuf 메시지와 gRPC 서비스
- transport: 인증 헤더, capability 탐색, HTTP/gRPC 공용 transport
- lifecycle: 배포 관리 세션 및 종료 상태 계약
- artifact: FAR 검증
- operations, store: 작업 영속화 및 복구

## 생성 및 검증

Go 1.25 이상. protoc와 protoc-gen-go, protoc-gen-go-grpc를 설치한 뒤:

```sh
./generate.sh
go test ./...
```

## 호환성

fatima-core v1.3.7의 opm을 이전했습니다. Go import 경로만 변경하며 protobuf package `fatima.opm.v2`, 서비스명, 필드 번호, JSON 저장 형식은 유지합니다. 기존 opm/api와 새 api를 한 바이너리에 함께 import하지 마세요. 두 패키지가 동일한 protobuf 이름을 등록합니다.

fatima-opm은 독립적으로 릴리스합니다. 기존 v1.x Fatima 프로그램은 변경할 필요가 없습니다.
