# release #

## v1.1.0 ##
- Identity.Login 응답(Session)에 profile 필드 추가 (필드 번호 5)
- Jupiter 실행 환경의 FATIMA_PROFILE 값을 전달해 fatima-cmd가 배포 환경에 맞게 FAR를 재조립할 수 있게 함
- 기존 클라이언트는 모르는 필드를 무시하므로 하위 호환 유지

## v1.0.0 ##
- fatima-core v1.3.7의 opm 패키지를 독립 Go 모듈로 이전
- api, transport, lifecycle, artifact, operations, store 제공
- 기존 운영 통신 규격과 작업 저장 형식 유지
- fatima-core 의존성 없이 운영 기능을 독립적으로 릴리스
